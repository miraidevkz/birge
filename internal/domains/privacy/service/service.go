// Package service implements privacy use cases: consents and account deletion.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/privacy/model"
	"birge/internal/domains/privacy/port"
	"birge/internal/platform/clock"
)

const defaultDeletionDueAfter = 24 * time.Hour

// Settings holds privacy runtime configuration.
type Settings struct {
	// CurrentVersions maps consent type -> current text version.
	CurrentVersions map[string]string
	// DeletionDueAfter is how long after a request the shredder runs (default 24h).
	DeletionDueAfter time.Duration
}

// Service is the privacy application service.
type Service struct {
	Store    port.Store
	Shredder port.KeyShredder
	Clock    clock.Clock
	Logger   *slog.Logger
	Settings Settings
}

func (s *Service) deletionDueAfter() time.Duration {
	if s.Settings.DeletionDueAfter > 0 {
		return s.Settings.DeletionDueAfter
	}
	return defaultDeletionDueAfter
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) currentVersion(consentType string) (string, bool) {
	if s.Settings.CurrentVersions == nil {
		return "", false
	}
	v, ok := s.Settings.CurrentVersions[consentType]
	return v, ok && v != ""
}

// HasConsent reports whether the member currently grants consentType for the
// configured current text version. Exported for other bounded contexts.
func (s *Service) HasConsent(ctx context.Context, memberID uuid.UUID, consentType string) (bool, error) {
	if memberID == uuid.Nil {
		return false, model.ErrUnauthorized
	}
	if !model.ConsentTypeIsValid(consentType) {
		return false, fmt.Errorf("%w: type", model.ErrInvalidInput)
	}
	current, ok := s.currentVersion(consentType)
	if !ok {
		return false, fmt.Errorf("%w: no current version for %s", model.ErrInvalidInput, consentType)
	}

	var record model.ConsentRecord
	var found bool
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		r, err := tx.GetLatestConsent(ctx, memberID, consentType)
		if errors.Is(err, model.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		record = r
		found = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return record.Granted && record.Version == current, nil
}

// ListConsents returns the current state of every configured consent type.
func (s *Service) ListConsents(ctx context.Context, memberID uuid.UUID) ([]model.ConsentState, error) {
	if memberID == uuid.Nil {
		return nil, model.ErrUnauthorized
	}

	var latest []model.ConsentRecord
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		latest, err = tx.ListLatestConsents(ctx, memberID)
		return err
	})
	if err != nil {
		return nil, err
	}

	byType := make(map[string]model.ConsentRecord, len(latest))
	for _, r := range latest {
		byType[r.Type] = r
	}

	out := make([]model.ConsentState, 0, len(model.AllConsentTypes))
	for _, t := range model.AllConsentTypes {
		current, ok := s.currentVersion(t)
		if !ok {
			continue
		}
		state := model.ConsentState{
			Type:           t,
			CurrentVersion: current,
			Granted:        false,
		}
		if r, ok := byType[t]; ok {
			changed := r.ChangedAt
			state.ChangedAt = &changed
			v := r.Version
			state.GrantedVersion = &v
			state.Granted = r.Granted && r.Version == current
		}
		out = append(out, state)
	}
	return out, nil
}

// SetConsent appends a consent history row for the current text version and
// emits privacy.ConsentChanged in the same transaction.
func (s *Service) SetConsent(ctx context.Context, memberID uuid.UUID, consentType, version string, granted bool) (model.ConsentState, error) {
	if memberID == uuid.Nil {
		return model.ConsentState{}, model.ErrUnauthorized
	}
	if err := model.ValidateConsentChange(consentType, version); err != nil {
		return model.ConsentState{}, err
	}
	current, ok := s.currentVersion(consentType)
	if !ok {
		return model.ConsentState{}, fmt.Errorf("%w: type", model.ErrInvalidInput)
	}
	if version != current {
		return model.ConsentState{}, fmt.Errorf("%w: version", model.ErrInvalidInput)
	}

	now := s.Clock.Now().UTC()
	var state model.ConsentState
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		rec, err := tx.AppendConsent(ctx, model.ConsentRecord{
			MemberID:  memberID,
			Type:      consentType,
			Version:   version,
			Granted:   granted,
			ChangedAt: now,
		})
		if err != nil {
			return err
		}
		if err := tx.PublishConsentChanged(ctx, now, model.ConsentChangedData{
			MemberID: memberID,
			Type:     consentType,
			Version:  version,
			Granted:  granted,
		}); err != nil {
			return err
		}
		gv := rec.Version
		changed := rec.ChangedAt
		state = model.ConsentState{
			Type:           consentType,
			Granted:        granted,
			GrantedVersion: &gv,
			CurrentVersion: current,
			ChangedAt:      &changed,
		}
		return nil
	})
	if err != nil {
		return model.ConsentState{}, err
	}
	s.log().Info("consent changed", "event", "ConsentChanged", "member_id", memberID, "type", consentType)
	return state, nil
}

// RequestDeletion records a deletion request (idempotent per member), emits
// privacy.DataDeletionRequested and schedules privacy.DeletionDue.
func (s *Service) RequestDeletion(ctx context.Context, memberID uuid.UUID) (model.DeletionRequest, error) {
	if memberID == uuid.Nil {
		return model.DeletionRequest{}, model.ErrUnauthorized
	}

	now := s.Clock.Now().UTC()
	dueAt := now.Add(s.deletionDueAfter())

	var out model.DeletionRequest
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		created, inserted, err := tx.InsertDeletionRequest(ctx, model.DeletionRequest{
			MemberID:    memberID,
			RequestedAt: now,
			DueAt:       dueAt,
		})
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := tx.GetDeletionRequest(ctx, memberID)
			if err != nil {
				return err
			}
			out = existing
			return nil
		}
		if err := tx.PublishDataDeletionRequested(ctx, now, model.DataDeletionRequestedData{
			MemberID: memberID,
			DueAt:    created.DueAt,
		}); err != nil {
			return err
		}
		if _, err := tx.ScheduleDeletionDue(ctx, memberID, created.DueAt); err != nil {
			return err
		}
		out = created
		s.log().Info("deletion requested", "event", "DataDeletionRequested", "member_id", memberID)
		return nil
	})
	return out, err
}

// HandleDeletionDue shreds the member key then marks the request completed.
// Order matters: shred first so a crash after Shred still retries mark-completed,
// while a completed row never means the DEK is still live. Idempotent: a
// completed request skips Shred.
func (s *Service) HandleDeletionDue(ctx context.Context, memberID uuid.UUID) error {
	if memberID == uuid.Nil {
		return fmt.Errorf("%w: member id", model.ErrInvalidInput)
	}

	var (
		skip  bool
		shred bool
		dueAt time.Time
	)
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		req, err := tx.GetDeletionRequest(ctx, memberID)
		if errors.Is(err, model.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			skip = true
			return nil
		}
		if err != nil {
			return err
		}
		if req.CompletedAt != nil {
			skip = true
			return nil
		}
		dueAt = req.DueAt
		shred = true
		return nil
	})
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	if !shred {
		return nil
	}

	if s.Shredder == nil {
		return errors.New("privacy: key shredder not configured")
	}
	if err := s.Shredder.Shred(ctx, memberID); err != nil {
		return fmt.Errorf("shred member key: %w", err)
	}

	now := s.Clock.Now().UTC()
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		_, marked, err := tx.MarkDeletionCompleted(ctx, memberID, now)
		if err != nil {
			return err
		}
		if !marked {
			// Another successful run completed first; treat as success.
			return nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.log().Info("deletion completed", "event", "DeletionCompleted", "member_id", memberID, "due_at", dueAt)
	return nil
}
