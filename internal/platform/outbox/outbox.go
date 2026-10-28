// Package outbox implements the transactional outbox (ADR-0007): events are
// written in the same transaction as the data and published to the broker
// later. It also carries traces across the broker (ADR-0021): the envelope's
// trace_parent is the producer's span, the relay publishes under it and
// TraceJetStream gives every consumed message a span under it.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"birge/internal/platform/store"
	"birge/internal/platform/telemetry"
)

var eventTypeRe = regexp.MustCompile(`^[a-z]+\.[A-Z][A-Za-z]+$`)

// Event is a domain event to publish. Data carries IDs and facts only, never
// message text or decrypted personal data.
type Event struct {
	Type    string // <context>.<EventName>, e.g. safety.EscalationOpened
	Version int
	Data    any
}

// Envelope is the wire format (contracts/events/envelope.v1.json).
type Envelope struct {
	ID          uuid.UUID       `json:"id"`
	Type        string          `json:"type"`
	Version     int             `json:"version"`
	OccurredAt  time.Time       `json:"occurred_at"`
	TraceParent string          `json:"trace_parent,omitempty"`
	Data        json.RawMessage `json:"data"`
}

// Enqueue stores e in the outbox using q, which must be bound to the caller's
// transaction (store.New(tx)). An empty traceParent takes the span of ctx
// (the traced request or message that caused the event), so consumers'
// spans join the producer's trace without every context passing it.
func Enqueue(ctx context.Context, q *store.Queries, now time.Time, traceParent string, e Event) (uuid.UUID, error) {
	if err := validate(e); err != nil {
		return uuid.Nil, err
	}
	if traceParent == "" {
		traceParent = telemetry.TraceParent(ctx)
	}
	data, err := json.Marshal(e.Data)
	if err != nil {
		return uuid.Nil, fmt.Errorf("outbox: marshal %s: %w", e.Type, err)
	}
	var tp *string
	if traceParent != "" {
		tp = &traceParent
	}
	id, err := q.InsertOutboxEvent(ctx, store.InsertOutboxEventParams{
		EventType:    e.Type,
		EventVersion: int32(e.Version), //nolint:gosec // validated to be in 1..1000
		Payload:      data,
		TraceParent:  tp,
		OccurredAt:   now,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("outbox: insert %s: %w", e.Type, err)
	}
	return id, nil
}

func validate(e Event) error {
	if !eventTypeRe.MatchString(e.Type) {
		return fmt.Errorf("outbox: invalid event type %q", e.Type)
	}
	if e.Version < 1 || e.Version > 1000 {
		return fmt.Errorf("outbox: invalid version %d for %s", e.Version, e.Type)
	}
	return nil
}

// Subject is the broker subject for an event type.
func Subject(eventType string) string { return "birge.events." + eventType }

// envelope builds the wire message for a stored row.
func envelope(r store.ClaimUnpublishedEventsRow) ([]byte, error) {
	env := Envelope{
		ID:         r.ID,
		Type:       r.EventType,
		Version:    int(r.EventVersion),
		OccurredAt: r.OccurredAt.UTC(),
		Data:       r.Payload,
	}
	if r.TraceParent != nil {
		env.TraceParent = *r.TraceParent
	}
	b, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	return b, nil
}
