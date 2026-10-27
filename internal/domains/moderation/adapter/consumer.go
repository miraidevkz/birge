package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/service"
	"birge/internal/platform/outbox"
)

// queueConsumer is the durable JetStream consumer name.
const queueConsumer = "moderation-queue"

// QueueConsumer consumes the five submission events (sharing.ListingSubmitted,
// directory.ItemSubmitted, chat.MessageHeld, chat.MessageReported,
// membership.VerificationRequested) and opens queue items. Accepting an event
// needs only the moderation database, so an outage of another context cannot
// exhaust redeliveries. Duplicates are dropped through processed_events; a
// malformed payload is terminated (redelivery cannot fix it) and logged by
// event ID only.
type QueueConsumer struct {
	JS      jetstream.JetStream
	Stream  string
	Service *service.Service
	Logger  *slog.Logger
}

// EnsureConsumer creates the durable consumer if needed.
func (c *QueueConsumer) EnsureConsumer(ctx context.Context) (jetstream.Consumer, error) {
	cons, err := c.JS.CreateOrUpdateConsumer(ctx, c.Stream, jetstream.ConsumerConfig{
		Durable: queueConsumer,
		FilterSubjects: []string{
			outbox.Subject(model.EventListingSubmitted),
			outbox.Subject(model.EventItemSubmitted),
			outbox.Subject(model.EventMessageHeld),
			outbox.Subject(model.EventMessageReported),
			outbox.Subject(model.EventVerificationRequested),
		},
		AckPolicy:  jetstream.AckExplicitPolicy,
		AckWait:    30 * time.Second,
		MaxDeliver: 10,
	})
	if err != nil {
		return nil, fmt.Errorf("create moderation queue consumer: %w", err)
	}
	return cons, nil
}

// HandleMessage processes one message body. It is safe to call with duplicates.
func (c *QueueConsumer) HandleMessage(ctx context.Context, body []byte) error {
	var env outbox.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("%w: envelope", model.ErrInvalidInput)
	}
	return c.Service.HandleEvent(ctx, env.ID, env.Type, env.Data)
}

// Run consumes until ctx is cancelled.
func (c *QueueConsumer) Run(ctx context.Context) error {
	cons, err := c.EnsureConsumer(ctx)
	if err != nil {
		return err
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := c.HandleMessage(ctx, msg.Data()); err != nil {
			if errors.Is(err, model.ErrInvalidInput) {
				c.log().Error("moderation queue: malformed event", "event", "ModerationEventMalformed", "subject", msg.Subject())
				_ = msg.Term()
				return
			}
			c.log().Error("moderation queue", "event", "ModerationQueueFailed", "error", err)
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

func (c *QueueConsumer) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}
