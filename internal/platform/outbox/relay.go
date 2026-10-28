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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"birge/internal/platform/clock"
	"birge/internal/platform/db"
	"birge/internal/platform/store"
	"birge/internal/platform/telemetry"
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
// anyway, ADR-0007). Each publish is a producer span under the event's
// trace_parent; birge_outbox_published_total counts events once committed.
func (r *Relay) PublishBatch(ctx context.Context) (int, error) {
	var published int
	var pubErr error
	var types []string
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
			if err := r.publish(ctx, row, body); err != nil {
				pubErr = fmt.Errorf("publish %s: %w", row.ID, err)
				break
			}
			ids = append(ids, row.ID)
			types = append(types, row.EventType)
		}
		if len(ids) == 0 {
			return nil
		}
		published = len(ids)
		return q.MarkEventsPublished(ctx, store.MarkEventsPublishedParams{PublishedAt: ptr(r.Clock.Now()), Ids: ids})
	})
	if err == nil {
		// Committed: these events are out and marked.
		for _, t := range types {
			telemetry.OutboxPublished.WithLabelValues(t).Inc()
		}
	}
	if err != nil || pubErr != nil {
		telemetry.OutboxPublishErrors.Inc()
	}
	return published, errors.Join(err, pubErr)
}

// publish sends one event inside a producer span whose parent is the
// event's trace_parent (none: a new root).
func (r *Relay) publish(ctx context.Context, row store.ClaimUnpublishedEventsRow, body []byte) error {
	if row.TraceParent != nil {
		ctx = telemetry.ContextWithTraceParent(ctx, *row.TraceParent)
	}
	subject := Subject(row.EventType)
	ctx, span := telemetry.Tracer().Start(ctx, "publish "+row.EventType, trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String(telemetry.KeyMessagingSystem, "nats"),
			attribute.String(telemetry.KeyMessagingOp, "publish"),
			attribute.String(telemetry.KeyMessagingDest, subject),
			attribute.String(telemetry.KeyMessagingMsgID, row.ID.String()),
			attribute.String(telemetry.KeyEventType, row.EventType),
		))
	defer span.End()
	if err := r.Publisher.Publish(ctx, Message{ID: row.ID, Subject: subject, Body: body}); err != nil {
		span.SetStatus(codes.Error, "")
		return err
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
