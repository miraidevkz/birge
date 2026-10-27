package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"birge/internal/domains/safety/model"
	"birge/internal/domains/safety/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/crypto"
)

const (
	queuePageSize   = 50
	rosterPageSize  = 500
	maxRosterWindow = 62 * 24 * time.Hour
	maxShiftLength  = 7 * 24 * time.Hour
)

var (
	// queueRoles may work the escalation queue.
	queueRoles = []string{model.RolePsychologist, model.RoleModerator}
	// rosterViewRoles may read the duty roster.
	rosterViewRoles = []string{model.RolePsychologist, model.RoleModerator, model.RoleAdmin}
	// rosterEditRoles may change the duty roster.
	rosterEditRoles = []string{model.RoleAdmin}
)

// Actor is an authorized staff member: the principal's id and the role that
// allowed the operation (recorded in the audit log).
type Actor struct {
	ID   uuid.UUID
	Role string
}

// authorize returns the actor when p is a staff principal holding one of
// allowed (deny by default). The first allowed role the principal holds is
// the audited role.
func authorize(p auth.Principal, allowed []string) (Actor, error) {
	if p.IdentityID == uuid.Nil {
		return Actor{}, model.ErrUnauthorized
	}
	if !p.Staff {
		return Actor{}, model.ErrForbidden
	}
	for _, want := range allowed {
		for _, have := range p.Roles {
			if have == want {
				return Actor{ID: p.IdentityID, Role: want}, nil
			}
		}
	}
	return Actor{}, model.ErrForbidden
}

// EscalationPage is one page of the staff queue.
type EscalationPage struct {
	Items      []model.Escalation
	NextCursor string
}

// ListEscalations returns the staff queue newest first, optionally filtered
// by status. Items carry source, time and district only (no name, no phone).
func (s *Service) ListEscalations(ctx context.Context, p auth.Principal, status, cursor string) (EscalationPage, error) {
	if _, err := authorize(p, queueRoles); err != nil {
		return EscalationPage{}, err
	}
	if status != "" && !model.StatusIsValid(status) {
		return EscalationPage{}, fmt.Errorf("%w: status", model.ErrInvalidInput)
	}
	f := model.EscalationFilter{Status: status, Limit: queuePageSize + 1}
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return EscalationPage{}, err
		}
		f.CursorOpenedAt, f.CursorID = &at, &id
	}
	var items []model.Escalation
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		items, err = tx.ListEscalations(ctx, f)
		return err
	}); err != nil {
		return EscalationPage{}, err
	}
	page := EscalationPage{Items: items}
	if len(items) > queuePageSize {
		page.Items = items[:queuePageSize]
		last := page.Items[queuePageSize-1]
		page.NextCursor = encodeCursor(last.OpenedAt, last.ID)
	}
	return page, nil
}

// encodeCursor makes an opaque keyset cursor from the last item of a page.
func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixNano(), 10) + "." + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	ns, idStr, ok := strings.Cut(string(raw), ".")
	if !ok {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	return time.Unix(0, n).UTC(), id, nil
}

// TakeEscalation makes the caller responsible for an open escalation: it
// cancels the backup timer, publishes safety.EscalationTaken and writes an
// audit entry, in one transaction. Taking an escalation the caller already
// took returns it unchanged; one taken by someone else or closed is
// model.ErrConflict.
func (s *Service) TakeEscalation(ctx context.Context, p auth.Principal, id uuid.UUID) (model.Escalation, error) {
	actor, err := authorize(p, queueRoles)
	if err != nil {
		return model.Escalation{}, err
	}
	now := s.now()
	var out model.Escalation
	var taken bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		e, updated, err := tx.TakeEscalation(ctx, id, actor.ID, now)
		if err != nil {
			return err
		}
		if !updated {
			cur, err := tx.GetEscalation(ctx, id, false)
			if err != nil {
				return err
			}
			if cur.Status == model.StatusTaken && cur.TakenByStaff(actor.ID) {
				out = cur
				return nil
			}
			return fmt.Errorf("%w: escalation is %s", model.ErrConflict, cur.Status)
		}
		if _, err := tx.CancelBackup(ctx, id, now); err != nil {
			return err
		}
		if err := tx.Publish(ctx, now, model.Event{
			Type:    model.EventEscalationTaken,
			Version: model.EventVersion,
			Data:    model.EscalationTakenData{EscalationID: id, TakenBy: actor.ID},
		}); err != nil {
			return err
		}
		if err := tx.Audit(ctx, auditEntry(now, actor, model.AuditTake, model.AuditObjectEscalation, id)); err != nil {
			return err
		}
		out, taken = e, true
		return nil
	})
	if err != nil {
		return model.Escalation{}, err
	}
	if taken {
		s.log().Info("escalation taken", "event", model.EventEscalationTaken, "escalation_id", id, "staff_id", actor.ID)
	}
	return out, nil
}

// RevealContact returns the member's name, language and phone to the staff
// member who took the escalation, and only while it is taken. The audit
// entry is committed before the contact is read, so every reveal is
// recorded even if the response is lost.
func (s *Service) RevealContact(ctx context.Context, p auth.Principal, id uuid.UUID) (model.Contact, error) {
	actor, err := authorize(p, queueRoles)
	if err != nil {
		return model.Contact{}, err
	}
	if s.Directory == nil {
		return model.Contact{}, errors.New("safety: member directory not configured")
	}
	now := s.now()
	var memberID uuid.UUID
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		e, err := tx.GetEscalation(ctx, id, false)
		if err != nil {
			return err
		}
		if e.Status != model.StatusTaken || !e.TakenByStaff(actor.ID) {
			return fmt.Errorf("%w: not taken by caller", model.ErrForbidden)
		}
		memberID = e.MemberID
		return tx.Audit(ctx, auditEntry(now, actor, model.AuditRevealContact, model.AuditObjectEscalation, id))
	})
	if err != nil {
		return model.Contact{}, err
	}
	c, err := s.Directory.Contact(ctx, memberID)
	if err != nil {
		return model.Contact{}, fmt.Errorf("member contact: %w", err)
	}
	s.log().Info("escalation contact revealed", "event", "EscalationContactRevealed", "escalation_id", id, "staff_id", actor.ID)
	return c, nil
}

// CloseEscalation closes an escalation the caller took, with an outcome and
// an optional note sealed under the member's data key. It publishes
// safety.EscalationClosed and writes an audit entry in one transaction.
// Closing an open or closed escalation is model.ErrConflict; one taken by
// someone else is model.ErrForbidden.
func (s *Service) CloseEscalation(ctx context.Context, p auth.Principal, id uuid.UUID, outcome string, note *string) (model.Escalation, error) {
	actor, err := authorize(p, queueRoles)
	if err != nil {
		return model.Escalation{}, err
	}
	if !model.OutcomeIsValid(outcome) {
		return model.Escalation{}, fmt.Errorf("%w: outcome", model.ErrInvalidInput)
	}
	if note != nil && (len(*note) > model.MaxNoteLen || !utf8.ValidString(*note)) {
		return model.Escalation{}, fmt.Errorf("%w: note", model.ErrInvalidInput)
	}
	hasNote := note != nil && strings.TrimSpace(*note) != ""
	if hasNote && s.Sealer == nil {
		return model.Escalation{}, errors.New("safety: sealer not configured")
	}
	now := s.now()
	var out model.Escalation
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		e, err := tx.GetEscalation(ctx, id, true)
		if err != nil {
			return err
		}
		switch {
		case e.Status != model.StatusTaken:
			return fmt.Errorf("%w: escalation is %s", model.ErrConflict, e.Status)
		case !e.TakenByStaff(actor.ID):
			return fmt.Errorf("%w: not taken by caller", model.ErrForbidden)
		}
		var sealed []byte
		if hasNote {
			sealed, err = s.Sealer.Seal(ctx, e.MemberID, []byte(*note), crypto.AssociatedData(model.FieldEscalationNote, id))
			if err != nil {
				return fmt.Errorf("seal note: %w", err)
			}
		}
		closed, updated, err := tx.CloseEscalation(ctx, id, actor.ID, outcome, sealed, now)
		if err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("%w: escalation changed", model.ErrConflict)
		}
		// The timer was cancelled on take; cancelling again is a no-op kept
		// as a guard so a closed escalation can never alert the backup.
		if _, err := tx.CancelBackup(ctx, id, now); err != nil {
			return err
		}
		if err := tx.Publish(ctx, now, model.Event{
			Type:    model.EventEscalationClosed,
			Version: model.EventVersion,
			Data:    model.EscalationClosedData{EscalationID: id, Outcome: outcome},
		}); err != nil {
			return err
		}
		if err := tx.Audit(ctx, auditEntry(now, actor, model.AuditClose, model.AuditObjectEscalation, id)); err != nil {
			return err
		}
		out = closed
		return nil
	})
	if err != nil {
		return model.Escalation{}, err
	}
	s.log().Info("escalation closed", "event", model.EventEscalationClosed, "escalation_id", id, "outcome", outcome)
	return out, nil
}

// DutyRoster returns the active shifts overlapping [from, to), at most 62 days.
func (s *Service) DutyRoster(ctx context.Context, p auth.Principal, from, to time.Time) ([]model.DutyShift, error) {
	if _, err := authorize(p, rosterViewRoles); err != nil {
		return nil, err
	}
	if !to.After(from) || to.Sub(from) > maxRosterWindow {
		return nil, fmt.Errorf("%w: period", model.ErrInvalidInput)
	}
	var out []model.DutyShift
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		out, err = tx.ListDutyShifts(ctx, from.UTC(), to.UTC(), rosterPageSize)
		return err
	})
	return out, err
}

// AddDutyShift adds a shift (admin only). The shift id is the request's
// Idempotency-Key: a repeated request returns the stored shift unchanged.
// The on-duty phone is sealed under the staff member's data key.
func (s *Service) AddDutyShift(ctx context.Context, p auth.Principal, in model.NewDutyShift) (model.DutyShift, error) {
	actor, err := authorize(p, rosterEditRoles)
	if err != nil {
		return model.DutyShift{}, err
	}
	now := s.now()
	if err := validateShift(in, now); err != nil {
		return model.DutyShift{}, err
	}
	if s.Sealer == nil {
		return model.DutyShift{}, errors.New("safety: sealer not configured")
	}
	phone, err := s.Sealer.Seal(ctx, in.StaffID, []byte(in.Phone), crypto.AssociatedData(model.FieldShiftPhone, in.ID))
	if err != nil {
		return model.DutyShift{}, fmt.Errorf("seal duty phone: %w", err)
	}
	var out model.DutyShift
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		created, inserted, err := tx.InsertDutyShift(ctx, model.DutyShift{
			ID:        in.ID,
			StaffID:   in.StaffID,
			Role:      in.Role,
			StartsAt:  in.StartsAt.UTC(),
			EndsAt:    in.EndsAt.UTC(),
			Backup:    in.Backup,
			Phone:     phone,
			CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !inserted {
			out, err = tx.GetDutyShift(ctx, in.ID)
			return err
		}
		out = created
		return tx.Audit(ctx, auditEntry(now, actor, model.AuditShiftAdd, model.AuditObjectDutyShift, in.ID))
	})
	if err != nil {
		return model.DutyShift{}, err
	}
	return out, nil
}

func validateShift(in model.NewDutyShift, now time.Time) error {
	switch {
	case in.ID == uuid.Nil || in.StaffID == uuid.Nil:
		return fmt.Errorf("%w: id", model.ErrInvalidInput)
	case !model.ShiftRoleIsValid(in.Role):
		return fmt.Errorf("%w: role", model.ErrInvalidInput)
	case !in.EndsAt.After(in.StartsAt) || in.EndsAt.Sub(in.StartsAt) > maxShiftLength:
		return fmt.Errorf("%w: period", model.ErrInvalidInput)
	case !in.EndsAt.After(now):
		return fmt.Errorf("%w: shift already ended", model.ErrInvalidInput)
	case !model.PhoneIsE164(in.Phone):
		return fmt.Errorf("%w: phone", model.ErrInvalidInput)
	}
	return nil
}

// CancelDutyShift cancels a shift that has not started (admin only). The row
// is kept with cancelled_at set; nothing is deleted. Cancelling twice is a
// no-op; a started shift is model.ErrConflict.
func (s *Service) CancelDutyShift(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	actor, err := authorize(p, rosterEditRoles)
	if err != nil {
		return err
	}
	now := s.now()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		_, updated, err := tx.CancelDutyShift(ctx, id, now)
		if err != nil {
			return err
		}
		if !updated {
			cur, err := tx.GetDutyShift(ctx, id)
			if err != nil {
				return err
			}
			if cur.CancelledAt != nil {
				return nil
			}
			return fmt.Errorf("%w: shift has started", model.ErrConflict)
		}
		return tx.Audit(ctx, auditEntry(now, actor, model.AuditShiftCancel, model.AuditObjectDutyShift, id))
	})
}

func auditEntry(at time.Time, a Actor, action, objectType string, objectID uuid.UUID) model.AuditEntry {
	return model.AuditEntry{At: at, ActorID: a.ID, ActorRole: a.Role, Action: action, ObjectType: objectType, ObjectID: objectID}
}
