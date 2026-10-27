package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/service"
	"birge/internal/platform/outbox"
)

const (
	moderationConsumerName = "sharing-moderation-decided"
	moderationFilter       = "birge.events.moderation.Decided"
	eventsStream           = "EVENTS"
)

// ModerationConsumer consumes moderation.Decided and applies listing
// decisions. Deduplicated in the service via platform.processed_events.
type ModerationConsumer struct {
	JS      jetstream.JetStream
	Service *service.Service
	Logger  *slog.Logger
}

// EnsureConsumer creates the durable consumer if needed.
func (c *ModerationConsumer) EnsureConsumer(ctx context.Context) (jetstream.Consumer, error) {
	cons, err := c.JS.CreateOrUpdateConsumer(ctx, eventsStream, jetstream.ConsumerConfig{
		Durable:       moderationConsumerName,
		FilterSubject: moderationFilter,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    10,
	})
	if err != nil {
		return nil, fmt.Errorf("create sharing moderation consumer: %w", err)
	}
	return cons, nil
}

// HandleMessage processes one JetStream message. Safe with duplicates.
func (c *ModerationConsumer) HandleMessage(ctx context.Context, body []byte) error {
	var env outbox.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}
	if env.Type != model.EventModerationDecided {
		c.log().Info("ignoring event", "event", env.Type, "event_id", env.ID)
		return nil
	}
	var data model.ModerationDecidedData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return fmt.Errorf("parse ModerationDecided: %w", err)
	}
	return c.Service.ApplyModerationDecided(ctx, env.ID, data)
}

// Run consumes until ctx is cancelled.
func (c *ModerationConsumer) Run(ctx context.Context) error {
	cons, err := c.EnsureConsumer(ctx)
	if err != nil {
		return err
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := c.HandleMessage(ctx, msg.Data()); err != nil {
			c.log().Error("sharing moderation decided", "event", "SharingModerationFailed", "error", err)
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

func (c *ModerationConsumer) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}
