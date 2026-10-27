// Package timers implements durable timers on PostgreSQL (ADR-0020). A timer
// is created in the same transaction as the domain change that needs it and
// fires at most once per successful handler run.
package timers

import (
	"context"
	"encoding/json"
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

// Timer is a request to run a handler of Kind at DueAt.
type Timer struct {
	Kind      string
	SubjectID uuid.UUID
	// DedupKey, when set, keeps at most one active timer per (Kind, DedupKey).
	DedupKey string
	DueAt    time.Time
	Payload  any
}

// Due is a timer handed to its handler.
type Due struct {
	ID        uuid.UUID
	Kind      string
	SubjectID uuid.UUID
	DueAt     time.Time
	Payload   json.RawMessage
	Attempts  int
}

// Handler runs a due timer inside tx. Side effects go through the outbox in
// the same tx; handlers must be idempotent.
type Handler func(ctx context.Context, tx pgx.Tx, t Due) error

// Schedule stores t using q, which must be bound to the caller's transaction.
// It reports false when an active timer with the same dedup key exists.
func Schedule(ctx context.Context, q *store.Queries, t Timer) (bool, error) {
	if t.Kind == "" || t.SubjectID == uuid.Nil || t.DueAt.IsZero() {
		return false, errors.New("timers: kind, subject and due time are required")
	}
	payload, err := json.Marshal(t.Payload)
	if err != nil {
		return false, fmt.Errorf("timers: marshal %s: %w", t.Kind, err)
	}
	var dedup *string
	if t.DedupKey != "" {
		dedup = &t.DedupKey
	}
	n, err := q.InsertTimer(ctx, store.InsertTimerParams{
		Kind: t.Kind, SubjectID: t.SubjectID, DedupKey: dedup, DueAt: t.DueAt.UTC(), Payload: payload,
	})
	if err != nil {
		return false, fmt.Errorf("timers: insert %s: %w", t.Kind, err)
	}
	return n == 1, nil
}

// Cancel marks active timers of kind for subject as done without firing them.
func Cancel(ctx context.Context, q *store.Queries, now time.Time, kind string, subject uuid.UUID) (int64, error) {
	return q.CancelTimers(ctx, store.CancelTimersParams{DoneAt: &now, Kind: kind, SubjectID: subject})
}

// Backoff is the delay before retry number attempts+1: 10s, 20s, 40s, ... capped at 1h.
func Backoff(attempts int) time.Duration {
	const base, ceiling = 10 * time.Second, time.Hour
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 9 {
		return ceiling
	}
	return min(base<<attempts, ceiling)
}

// Worker fires due timers. Several replicas can run it; SKIP LOCKED gives each
// timer to one of them.
type Worker struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Logger   *slog.Logger
	Handlers map[string]Handler
	Interval time.Duration
}

// Run fires timers until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		for {
			fired, err := w.RunOnce(ctx)
			if err != nil && ctx.Err() == nil {
				w.Logger.Error("timer", "error", err)
			}
			if !fired || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce claims and runs one due timer. It reports whether a timer was claimed.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	var claimed *store.ClaimDueTimerRow
	var handlerErr error
	err := db.InTx(ctx, w.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := w.Clock.Now()
		row, err := q.ClaimDueTimer(ctx, now)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim timer: %w", err)
		}
		claimed = &row
		h, ok := w.Handlers[row.Kind]
		if !ok {
			handlerErr = fmt.Errorf("no handler for timer kind %q", row.Kind)
			return handlerErr
		}
		due := Due{ID: row.ID, Kind: row.Kind, SubjectID: row.SubjectID, DueAt: row.DueAt, Payload: row.Payload, Attempts: int(row.Attempts)}
		if err := h(ctx, tx, due); err != nil {
			handlerErr = fmt.Errorf("timer %s (%s): %w", row.ID, row.Kind, err)
			return handlerErr
		}
		return q.CompleteTimer(ctx, store.CompleteTimerParams{DoneAt: &now, ID: row.ID})
	})
	if claimed == nil {
		return false, err
	}
	if handlerErr != nil {
		// The handler's transaction was rolled back; record the failure and retry later.
		msg := handlerErr.Error()
		rerr := store.New(w.Pool).RescheduleTimer(ctx, store.RescheduleTimerParams{
			DueAt: w.Clock.Now().Add(Backoff(int(claimed.Attempts))), LastError: &msg, ID: claimed.ID,
		})
		return true, errors.Join(handlerErr, rerr)
	}
	return true, err
}
