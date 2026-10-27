// Package adapter implements the moderation ports on PostgreSQL (schema
// moderation, sqlc in ../store) with the platform hooks it needs (outbox,
// audit_log, processed_events), and the JetStream consumer of the five
// submission events. ContentSource adapters for the owning contexts are
// wired later.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/port"
	moderationstore "birge/internal/domains/moderation/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
)

// PostgresStore implements port.Store on PostgreSQL.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// InTx runs fn in a transaction.
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(Bind(pgxTx))
	})
}

// Bind returns a port.Tx working inside an existing transaction.
func Bind(tx pgx.Tx) port.Tx {
	return &postgresTx{q: moderationstore.New(tx), platform: platformstore.New(tx)}
}

type postgresTx struct {
	q        *moderationstore.Queries
	platform *platformstore.Queries
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// MarkProcessed implements port.Tx.
func (t *postgresTx) MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (bool, error) {
	n, err := t.platform.MarkEventProcessed(ctx, platformstore.MarkEventProcessedParams{
		Consumer: consumer, EventID: eventID,
	})
	if err != nil {
		return false, fmt.Errorf("mark processed: %w", err)
	}
	return n == 1, nil
}

// InsertItem implements port.Tx.
func (t *postgresTx) InsertItem(ctx context.Context, it model.Item) (bool, error) {
	_, err := t.q.InsertItem(ctx, moderationstore.InsertItemParams{
		ID: it.ID, Kind: string(it.Kind), ObjectID: it.ObjectID,
		SourceReason: optString(it.SourceReason), CreatedAt: it.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert item: %w", err)
	}
	return true, nil
}

// UpsertReportItem implements port.Tx.
func (t *postgresTx) UpsertReportItem(ctx context.Context, it model.Item) (uuid.UUID, int, error) {
	row, err := t.q.UpsertReportItem(ctx, moderationstore.UpsertReportItemParams{
		ID: it.ID, ObjectID: it.ObjectID, SourceReason: optString(it.SourceReason), CreatedAt: it.CreatedAt,
	})
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("upsert report item: %w", err)
	}
	return row.ID, int(row.ReportCount), nil
}

// GetItemForUpdate implements port.Tx.
func (t *postgresTx) GetItemForUpdate(ctx context.Context, id uuid.UUID) (model.Item, error) {
	row, err := t.q.GetItemForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Item{}, model.ErrNotFound
	}
	if err != nil {
		return model.Item{}, fmt.Errorf("get item: %w", err)
	}
	return mapItem(row), nil
}

// DecideItem implements port.Tx.
func (t *postgresTx) DecideItem(ctx context.Context, id, by uuid.UUID, at time.Time, d model.Decision) (bool, error) {
	approve := d.Approve
	_, err := t.q.DecideItem(ctx, moderationstore.DecideItemParams{
		DecidedBy: &by, DecidedAt: &at, Approve: &approve, Reason: optString(d.Reason),
		BlockAuthor: d.BlockAuthor, LegalHold: d.LegalHold, ID: id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("decide item: %w", err)
	}
	return true, nil
}

// ListOpenItems implements port.Tx.
func (t *postgresTx) ListOpenItems(ctx context.Context, f port.ListFilter) ([]model.Item, error) {
	p := moderationstore.ListOpenItemsParams{FilterKind: optString(string(f.Kind)), PageLimit: f.Limit}
	if !f.CursorCreatedAt.IsZero() {
		at, id := f.CursorCreatedAt, f.CursorID
		p.CursorCreatedAt, p.CursorID = &at, &id
	}
	rows, err := t.q.ListOpenItems(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list open items: %w", err)
	}
	out := make([]model.Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapItem(r))
	}
	return out, nil
}

// Publish implements port.Tx.
func (t *postgresTx) Publish(ctx context.Context, now time.Time, e model.Event) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{Type: e.Type, Version: e.Version, Data: e.Data})
	return err
}

// Audit implements port.Tx.
func (t *postgresTx) Audit(ctx context.Context, e port.AuditEntry) error {
	return audit.Record(ctx, t.platform, audit.Entry{
		ActorID: e.ActorID, ActorRole: e.ActorRole, Action: e.Action,
		ObjectType: e.ObjectType, ObjectID: e.ObjectID, Reason: e.Reason,
	})
}

func mapItem(r moderationstore.ModerationItem) model.Item {
	it := model.Item{
		ID: r.ID, Kind: model.Kind(r.Kind), ObjectID: r.ObjectID, Status: r.Status,
		ReportCount: int(r.ReportCount), CreatedAt: r.CreatedAt,
		Decision: model.Decision{BlockAuthor: r.BlockAuthor, LegalHold: r.LegalHold},
	}
	if r.SourceReason != nil {
		it.SourceReason = *r.SourceReason
	}
	if r.DecidedBy != nil {
		it.DecidedBy = *r.DecidedBy
	}
	if r.DecidedAt != nil {
		it.DecidedAt = *r.DecidedAt
	}
	if r.Approve != nil {
		it.Decision.Approve = *r.Approve
	}
	if r.Reason != nil {
		it.Decision.Reason = *r.Reason
	}
	return it
}
