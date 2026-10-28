package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/outbox"
)

// mentorRoleConsumer is the durable JetStream consumer name.
const mentorRoleConsumer = "chat-mentor-role-changed"

// MentorRoleConsumer consumes membership.MentorRoleChanged and, when the
// role was taken away, ends the member's room mentor assignments. It needs
// only the chat database, so an outage elsewhere cannot exhaust its
// redeliveries. Duplicates are dropped through platform.processed_events.
type MentorRoleConsumer struct {
	JS      jetstream.JetStream
	Stream  string
	Service *service.Service
	Logger  *slog.Logger
}

// EnsureConsumer creates the durable consumer if needed.
func (c *MentorRoleConsumer) EnsureConsumer(ctx context.Context) (jetstream.Consumer, error) {
	cons, err := c.JS.CreateOrUpdateConsumer(ctx, c.Stream, jetstream.ConsumerConfig{
		Durable:       mentorRoleConsumer,
		FilterSubject: outbox.Subject(model.EventMentorRoleChanged),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    10,
	})
	if err != nil {
		return nil, fmt.Errorf("create chat mentor role consumer: %w", err)
	}
	return cons, nil
}

// HandleMessage processes one message body. It is safe to call with duplicates.
func (c *MentorRoleConsumer) HandleMessage(ctx context.Context, body []byte) error {
	var env outbox.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}
	if env.Type != model.EventMentorRoleChanged {
		c.log().Info("ignoring event", "event", env.Type, "event_id", env.ID)
		return nil
	}
	var data model.MentorRoleChangedData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return fmt.Errorf("parse MentorRoleChanged: %w", err)
	}
	return c.Service.ApplyMentorRoleChanged(ctx, env.ID, data)
}

// Run consumes until ctx is cancelled.
func (c *MentorRoleConsumer) Run(ctx context.Context) error {
	cons, err := c.EnsureConsumer(ctx)
	if err != nil {
		return err
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := c.HandleMessage(ctx, msg.Data()); err != nil {
			c.log().Error("chat mentor role changed", "event", "ChatMentorRoleFailed", "error", err)
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

func (c *MentorRoleConsumer) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}
