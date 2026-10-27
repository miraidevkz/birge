package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/platform/clock"
	"birge/internal/platform/db"
	"birge/internal/platform/store"
)

// Message is one event ready for the broker.
type Message struct {
	ID      uuid.UUID // broker deduplication key
	Subject string
	Body    []byte
}

// Publisher sends a message to the broker and returns once it is persisted there.
type Publisher interface {
	Publish(ctx context.Context, m Message) error
}

// Relay moves events from the outbox to the broker.
type Relay struct {
	Pool      *pgxpool.Pool
	Publisher Publisher
	Clock     clock.Clock
	Logger    *slog.Logger
	BatchSize int32
	Interval  time.Duration
}

// Run publishes pending events until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		n, err := r.PublishBatch(ctx)
		if err != nil && ctx.Err() == nil {
			r.Logger.Error("outbox relay", "error", err)
		}
		if n == int(r.BatchSize) && err == nil {
			continue // more may be waiting
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// PublishBatch publishes up to BatchSize events. Events published before an
// error are still marked, so they are not sent again (consumers deduplicate
// anyway, ADR-0007).
func (r *Relay) PublishBatch(ctx context.Context) (int, error) {
	var published int
	var pubErr error
	err := db.InTx(ctx, r.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.ClaimUnpublishedEvents(ctx, r.BatchSize)
		if err != nil {
			return fmt.Errorf("claim events: %w", err)
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			body, err := envelope(row)
			if err != nil {
				pubErr = fmt.Errorf("envelope %s: %w", row.ID, err)
				break
			}
			if err := r.Publisher.Publish(ctx, Message{ID: row.ID, Subject: Subject(row.EventType), Body: body}); err != nil {
				pubErr = fmt.Errorf("publish %s: %w", row.ID, err)
				break
			}
			ids = append(ids, row.ID)
		}
		if len(ids) == 0 {
			return nil
		}
		published = len(ids)
		return q.MarkEventsPublished(ctx, store.MarkEventsPublishedParams{PublishedAt: ptr(r.Clock.Now()), Ids: ids})
	})
	return published, errors.Join(err, pubErr)
}

func ptr[T any](v T) *T { return &v }
