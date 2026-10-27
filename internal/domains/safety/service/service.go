// Package service implements the safety use cases (ADR-0023): crisis
// detection for other contexts, the escalation lifecycle open -> taken ->
// closed with its 15-minute backup timer, duty-roster management and the
// resolution of staff roles to on-duty phones for notify.
//
// Other contexts import only this package. They may rely on:
//
//   - Detect: the level-1 lexicon detector, pure and local;
//   - OpenEscalation: idempotent per member, and the escalation, its
//     safety.EscalationOpened event, the critical staff alert and the backup
//     timer are committed in one transaction or not at all;
//   - ResolveStaffRole: the external recipients notify sends critical SMS to.
//
// Authorization is checked here, not only in the HTTP layer. Logs carry IDs
// and event names only: never message text, names, phones or notes.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/safety/crisis"
	"birge/internal/domains/safety/model"
	"birge/internal/domains/safety/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
	"birge/internal/platform/crypto"
)

const (
	defaultBackupAfter = 15 * time.Minute
	defaultStaffLang   = "ru"
	// districtLookupTimeout bounds the optional district lookup so a slow
	// membership context cannot delay opening an escalation.
	districtLookupTimeout = 2 * time.Second
	// openAttempts bounds the retry when the member's active escalation is
	// closed between our conflicting insert and the read of the existing row.
	openAttempts = 3
)

// CrisisResult is the result of Detect (re-exported from the crisis package,
// which other contexts cannot import).
type CrisisResult = crisis.Result

// ExternalRecipient is one on-duty shift resolved for notify.
type ExternalRecipient = model.ExternalRecipient

// Settings is the safety runtime configuration.
type Settings struct {
	// BackupAfter is how long an escalation may stay open before the backup
	// shift is alerted (default 15 minutes, docs/requirements/mvp.ru.md § 5).
	BackupAfter time.Duration
	// CrisisPhones are the crisis lines returned with a help request; they
	// come from configuration per country, never from code.
	CrisisPhones []model.CrisisPhone
	// StaffLang is the language of SMS alerts to on-duty phones (default "ru").
	StaffLang string
}

// Service is the safety application service.
type Service struct {
	Store     port.Store
	Sealer    port.Sealer
	Directory port.MemberDirectory
	// Lexicon is the loaded crisis lexicon. Wiring must refuse to start when
	// it cannot be loaded; a nil Lexicon detects nothing and logs an error.
	Lexicon  *crisis.Lexicon
	Clock    clock.Clock
	Logger   *slog.Logger
	Settings Settings
}

var errRetryOpen = errors.New("safety: active escalation changed, retry")

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) backupAfter() time.Duration {
	if s.Settings.BackupAfter > 0 {
		return s.Settings.BackupAfter
	}
	return defaultBackupAfter
}

func (s *Service) staffLang() string {
	if s.Settings.StaffLang != "" {
		return s.Settings.StaffLang
	}
	return defaultStaffLang
}

// Detect runs the level-1 crisis lexicon on text. lang is the member's
// language and only sets the order in which languages are checked. It never
// logs or stores the text and does no I/O.
func (s *Service) Detect(text, lang string) CrisisResult {
	if s.Lexicon == nil {
		s.log().Error("crisis lexicon not loaded", "event", "LexiconMissing")
		return CrisisResult{}
	}
	return s.Lexicon.Detect(text, lang)
}

// CrisisPhones returns the configured crisis lines.
func (s *Service) CrisisPhones() []model.CrisisPhone {
	return slices.Clone(s.Settings.CrisisPhones)
}

// OpenEscalation opens an escalation for the member, or returns the one that
// is already open or taken (alreadyOpen = true; nothing else happens, so a
// repeated button press or a second detector hit alerts nobody twice).
//
// A new escalation is inserted together with safety.EscalationOpened, the
// critical notify.DeliveryRequested to the on-duty psychologists and the
// safety.EscalationBackup timer, in one transaction. It never depends on the
// LLM; the member's district is looked up best-effort and left empty when the
// directory is unavailable.
func (s *Service) OpenEscalation(ctx context.Context, memberID uuid.UUID, source string) (escalationID uuid.UUID, alreadyOpen bool, err error) {
	if memberID == uuid.Nil {
		return uuid.Nil, false, fmt.Errorf("%w: member id", model.ErrInvalidInput)
	}
	if !model.SourceIsValid(source) {
		return uuid.Nil, false, fmt.Errorf("%w: source", model.ErrInvalidInput)
	}
	district := s.district(ctx, memberID)
	for range openAttempts {
		escalationID, alreadyOpen, err = s.openOnce(ctx, memberID, source, district)
		if !errors.Is(err, errRetryOpen) {
			return escalationID, alreadyOpen, err
		}
	}
	return uuid.Nil, false, errors.New("safety: open escalation: active escalation kept changing")
}

func (s *Service) openOnce(ctx context.Context, memberID uuid.UUID, source string, district *uuid.UUID) (uuid.UUID, bool, error) {
	now := s.now()
	var id uuid.UUID
	var already bool
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		created, inserted, err := tx.InsertEscalation(ctx, model.Escalation{
			MemberID:   memberID,
			Source:     source,
			Status:     model.StatusOpen,
			DistrictID: district,
			OpenedAt:   now,
		})
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := tx.GetActiveEscalation(ctx, memberID)
			if errors.Is(err, model.ErrNotFound) {
				return errRetryOpen
			}
			if err != nil {
				return err
			}
			id, already = existing.ID, true
			return nil
		}
		id = created.ID
		if err := tx.Publish(ctx, now, model.Event{
			Type:    model.EventEscalationOpened,
			Version: model.EventVersion,
			Data:    model.EscalationOpenedData{EscalationID: id, MemberID: memberID, Source: source},
		}); err != nil {
			return err
		}
		if err := tx.Publish(ctx, now, model.StaffAlert(id, model.RecipientPsychologist, model.TemplateEscalationOpened)); err != nil {
			return err
		}
		if _, err := tx.ScheduleBackup(ctx, id, now.Add(s.backupAfter())); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	if already {
		s.log().Info("escalation already open", "event", "EscalationAlreadyOpen", "escalation_id", id)
	} else {
		// No member id in the log: an escalation is health data.
		s.log().Info("escalation opened", "event", model.EventEscalationOpened, "escalation_id", id, "source", source)
	}
	return id, already, nil
}

func (s *Service) district(ctx context.Context, memberID uuid.UUID) *uuid.UUID {
	if s.Directory == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, districtLookupTimeout)
	defer cancel()
	d, err := s.Directory.District(ctx, memberID)
	if err != nil {
		s.log().Warn("member district unavailable", "event", "DistrictLookupFailed", "error", err)
		return nil
	}
	return d
}

// HelpAccepted is the answer to a member's help request.
type HelpAccepted struct {
	EscalationID uuid.UUID
	CrisisPhones []model.CrisisPhone
}

// RequestHumanHelp opens (or returns) the calling member's escalation and
// returns the crisis lines. Members only; staff sessions are refused.
func (s *Service) RequestHumanHelp(ctx context.Context, p auth.Principal, source string) (HelpAccepted, error) {
	// Escalations are keyed by the membership member ID, never by the
	// identity provider's ID (auth.Principal.MemberID).
	if p.Staff || p.MemberID == uuid.Nil {
		return HelpAccepted{}, model.ErrUnauthorized
	}
	id, _, err := s.OpenEscalation(ctx, p.MemberID, source)
	if err != nil {
		return HelpAccepted{}, err
	}
	return HelpAccepted{EscalationID: id, CrisisPhones: s.CrisisPhones()}, nil
}

// ResolveStaffRole resolves a notify staff_role recipient to the shifts on
// duty now: "psychologist" (primary psychologist shifts), "psychologist_backup"
// (backup psychologist shifts) or "moderator". Each shift is one external
// recipient with its on-duty phone, so notify sends an SMS at once. It
// returns model.ErrNoDutyCoverage (and logs NoDutyCoverage) when no shift
// covers now. A shift whose phone cannot be opened is skipped and logged.
func (s *Service) ResolveStaffRole(ctx context.Context, role string) ([]ExternalRecipient, error) {
	var shiftRole string
	var backup bool
	switch role {
	case model.RecipientPsychologist:
		shiftRole = model.RolePsychologist
	case model.RecipientPsychologistBackup:
		shiftRole, backup = model.RolePsychologist, true
	case model.RecipientModerator:
		shiftRole = model.RoleModerator
	default:
		return nil, fmt.Errorf("%w: staff role", model.ErrInvalidInput)
	}
	if s.Sealer == nil {
		return nil, errors.New("safety: sealer not configured")
	}
	now := s.now()
	var shifts []model.DutyShift
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		shifts, err = tx.ListCoveringShifts(ctx, shiftRole, backup, now)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]ExternalRecipient, 0, len(shifts))
	for _, sh := range shifts {
		phone, err := s.Sealer.Open(ctx, sh.StaffID, sh.Phone, crypto.AssociatedData(model.FieldShiftPhone, sh.ID))
		if err != nil {
			s.log().Error("duty phone unavailable", "event", "DutyPhoneUnavailable", "shift_id", sh.ID, "error", err)
			continue
		}
		out = append(out, ExternalRecipient{Ref: sh.ID, PhoneE164: string(phone), Lang: s.staffLang()})
	}
	if len(out) == 0 {
		s.log().Error("no duty coverage", "event", "NoDutyCoverage", "role", role)
		return nil, fmt.Errorf("%w: %s", model.ErrNoDutyCoverage, role)
	}
	return out, nil
}

// HandleEscalationBackup runs the safety.EscalationBackup timer inside the
// timer worker's transaction tx. If the escalation is still open, it
// publishes safety.EscalationUnattended and a critical alert to the backup
// psychologists; if it was taken or closed (or is unknown), it does nothing.
// Idempotent: the row is locked and its status checked, and the alert's
// request id is deterministic.
func (s *Service) HandleEscalationBackup(ctx context.Context, tx port.Tx, escalationID uuid.UUID) error {
	e, err := tx.GetEscalation(ctx, escalationID, true)
	if errors.Is(err, model.ErrNotFound) {
		s.log().Warn("backup timer for unknown escalation", "event", "EscalationBackupOrphan", "escalation_id", escalationID)
		return nil
	}
	if err != nil {
		return err
	}
	if e.Status != model.StatusOpen {
		return nil
	}
	now := s.now()
	minutes := max(int(now.Sub(e.OpenedAt)/time.Minute), 0)
	if err := tx.Publish(ctx, now, model.Event{
		Type:    model.EventEscalationUnattended,
		Version: model.EventVersion,
		Data:    model.EscalationUnattendedData{EscalationID: e.ID, MinutesOpen: minutes},
	}); err != nil {
		return err
	}
	if err := tx.Publish(ctx, now, model.StaffAlert(e.ID, model.RecipientPsychologistBackup, model.TemplateEscalationUnattended)); err != nil {
		return err
	}
	s.log().Warn("escalation unattended", "event", model.EventEscalationUnattended, "escalation_id", e.ID, "minutes_open", minutes)
	return nil
}
