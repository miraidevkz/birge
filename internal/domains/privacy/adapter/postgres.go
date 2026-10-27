// Package adapter implements privacy ports on PostgreSQL and platform hooks
// (outbox events and the deletion-due timer).
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/privacy/model"
	"birge/internal/domains/privacy/port"
	privacystore "birge/internal/domains/privacy/store"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// PostgresStore implements port.Store on PostgreSQL (privacy schema + platform hooks).
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// InTx runs fn in a transaction.
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(&postgresTx{
			privacy:  privacystore.New(pgxTx),
			platform: platformstore.New(pgxTx),
		})
	})
}

type postgresTx struct {
	privacy  *privacystore.Queries
	platform *platformstore.Queries
}

// AppendConsent inserts one append-only consent_history row and returns it with its id.
func (t *postgresTx) AppendConsent(ctx context.Context, r model.ConsentRecord) (model.ConsentRecord, error) {
	row, err := t.privacy.AppendConsent(ctx, privacystore.AppendConsentParams{
		MemberID:  r.MemberID,
		Type:      r.Type,
		Version:   r.Version,
		Granted:   r.Granted,
		ChangedAt: r.ChangedAt,
	})
	if err != nil {
		return model.ConsentRecord{}, fmt.Errorf("append consent: %w", err)
	}
	return mapConsent(row), nil
}

// GetLatestConsent returns the newest history row for memberID/type or model.ErrNotFound.
func (t *postgresTx) GetLatestConsent(ctx context.Context, memberID uuid.UUID, consentType string) (model.ConsentRecord, error) {
	row, err := t.privacy.GetLatestConsent(ctx, privacystore.GetLatestConsentParams{
		MemberID: memberID,
		Type:     consentType,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ConsentRecord{}, model.ErrNotFound
	}
	if err != nil {
		return model.ConsentRecord{}, err
	}
	return mapConsent(row), nil
}

// ListLatestConsents returns the newest history row per consent type for the member.
func (t *postgresTx) ListLatestConsents(ctx context.Context, memberID uuid.UUID) ([]model.ConsentRecord, error) {
	rows, err := t.privacy.ListLatestConsents(ctx, memberID)
	if err != nil {
		return nil, err
	}
	out := make([]model.ConsentRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapConsent(row))
	}
	return out, nil
}

// InsertDeletionRequest creates the member's deletion request; inserted is false when one exists.
func (t *postgresTx) InsertDeletionRequest(ctx context.Context, r model.DeletionRequest) (model.DeletionRequest, bool, error) {
	row, err := t.privacy.InsertDeletionRequest(ctx, privacystore.InsertDeletionRequestParams{
		MemberID:    r.MemberID,
		RequestedAt: r.RequestedAt,
		DueAt:       r.DueAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.DeletionRequest{}, false, nil
	}
	if err != nil {
		return model.DeletionRequest{}, false, fmt.Errorf("insert deletion request: %w", err)
	}
	return mapDeletion(row), true, nil
}

// GetDeletionRequest returns the member's deletion request or model.ErrNotFound.
func (t *postgresTx) GetDeletionRequest(ctx context.Context, memberID uuid.UUID) (model.DeletionRequest, error) {
	row, err := t.privacy.GetDeletionRequest(ctx, memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.DeletionRequest{}, model.ErrNotFound
	}
	if err != nil {
		return model.DeletionRequest{}, err
	}
	return mapDeletion(row), nil
}

// MarkDeletionCompleted sets completed_at once; updated is false when absent or already completed.
func (t *postgresTx) MarkDeletionCompleted(ctx context.Context, memberID uuid.UUID, at time.Time) (model.DeletionRequest, bool, error) {
	row, err := t.privacy.MarkDeletionCompleted(ctx, privacystore.MarkDeletionCompletedParams{
		CompletedAt: &at,
		MemberID:    memberID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.DeletionRequest{}, false, nil
	}
	if err != nil {
		return model.DeletionRequest{}, false, fmt.Errorf("mark deletion completed: %w", err)
	}
	return mapDeletion(row), true, nil
}

// PublishConsentChanged enqueues privacy.ConsentChanged in the outbox of this transaction.
func (t *postgresTx) PublishConsentChanged(ctx context.Context, now time.Time, data model.ConsentChangedData) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{
		Type:    model.EventConsentChanged,
		Version: model.EventConsentChangedVersion,
		Data:    data,
	})
	return err
}

// PublishDataDeletionRequested enqueues privacy.DataDeletionRequested in this transaction's outbox.
func (t *postgresTx) PublishDataDeletionRequested(ctx context.Context, now time.Time, data model.DataDeletionRequestedData) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{
		Type:    model.EventDataDeletionRequested,
		Version: model.EventDataDeletionVersion,
		Data:    data,
	})
	return err
}

// ScheduleDeletionDue enqueues privacy.DeletionDue (dedup key = member id); scheduled is false on dedup hit.
func (t *postgresTx) ScheduleDeletionDue(ctx context.Context, memberID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind:      model.TimerDeletionDue,
		SubjectID: memberID,
		DedupKey:  memberID.String(),
		DueAt:     dueAt,
		Payload:   map[string]string{},
	})
}

func mapConsent(row privacystore.PrivacyConsentHistory) model.ConsentRecord {
	return model.ConsentRecord{
		ID:        row.ID,
		MemberID:  row.MemberID,
		Type:      row.Type,
		Version:   row.Version,
		Granted:   row.Granted,
		ChangedAt: row.ChangedAt,
	}
}

func mapDeletion(row privacystore.PrivacyDeletionRequest) model.DeletionRequest {
	return model.DeletionRequest{
		MemberID:    row.MemberID,
		RequestedAt: row.RequestedAt,
		DueAt:       row.DueAt,
		CompletedAt: row.CompletedAt,
	}
}

var _ port.Store = (*PostgresStore)(nil)
var _ port.Tx = (*postgresTx)(nil)
