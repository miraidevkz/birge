package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/service"
	"birge/internal/platform/outbox"
)

// factsConsumer is the durable JetStream consumer name.
const factsConsumer = "analytics-facts"

// FactsConsumer consumes the metric events of the ADR-0024 addendum
// (model.ConsumedEvents) and stores their facts. Accepting an event needs
// only the analytics database, so an outage elsewhere cannot exhaust its
// redeliveries. Duplicates are dropped through processed_events; a malformed
// payload is terminated (redelivery cannot fix it) and logged by subject
// only, never by payload.
type FactsConsumer struct {
	JS      jetstream.JetStream
	Stream  string
	Service *service.Service
	Logger  *slog.Logger
}

// EnsureConsumer creates the durable consumer if needed.
func (c *FactsConsumer) EnsureConsumer(ctx context.Context) (jetstream.Consumer, error) {
	subjects := make([]string, 0, len(model.ConsumedEvents))
	for _, t := range model.ConsumedEvents {
		subjects = append(subjects, outbox.Subject(t))
	}
	cons, err := c.JS.CreateOrUpdateConsumer(ctx, c.Stream, jetstream.ConsumerConfig{
		Durable:        factsConsumer,
		FilterSubjects: subjects,
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        30 * time.Second,
		MaxDeliver:     10,
	})
	if err != nil {
		return nil, fmt.Errorf("create analytics facts consumer: %w", err)
	}
	return cons, nil
}

// HandleMessage processes one message body. It is safe to call with duplicates.
func (c *FactsConsumer) HandleMessage(ctx context.Context, body []byte) error {
	var env outbox.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("%w: envelope", model.ErrInvalidInput)
	}
	return c.Service.HandleEvent(ctx, model.Event{ID: env.ID, Type: env.Type, OccurredAt: env.OccurredAt, Data: env.Data})
}

// Run consumes until ctx is cancelled.
func (c *FactsConsumer) Run(ctx context.Context) error {
	cons, err := c.EnsureConsumer(ctx)
	if err != nil {
		return err
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := c.HandleMessage(ctx, msg.Data()); err != nil {
			if errors.Is(err, model.ErrInvalidInput) {
				c.log().Error("analytics: malformed event", "event", "AnalyticsEventMalformed", "subject", msg.Subject())
				_ = msg.Term()
				return
			}
			c.log().Error("analytics facts", "event", "AnalyticsFactsFailed", "error", err)
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	defer cc.Stop()
	<-ctx.Done()
	return nil
}

func (c *FactsConsumer) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}
