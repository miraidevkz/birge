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

// roomOpenerConsumer is the durable JetStream consumer name.
const roomOpenerConsumer = "chat-direct-rooms"

// RoomOpenerConsumer consumes circle.MatchAccepted,
// emergency.SosResponseAccepted and sharing.ContactAccepted and opens one
// direct room per origin and pair. Accepting an event needs only the chat
// database (no other context), so an outage elsewhere cannot exhaust its
// redeliveries. Duplicates are dropped through platform.processed_events.
type RoomOpenerConsumer struct {
	JS      jetstream.JetStream
	Stream  string
	Service *service.Service
	Logger  *slog.Logger
}

// EnsureConsumer creates the durable consumer if needed.
func (c *RoomOpenerConsumer) EnsureConsumer(ctx context.Context) (jetstream.Consumer, error) {
	cons, err := c.JS.CreateOrUpdateConsumer(ctx, c.Stream, jetstream.ConsumerConfig{
		Durable: roomOpenerConsumer,
		FilterSubjects: []string{
			outbox.Subject(model.EventMatchAccepted),
			outbox.Subject(model.EventSosResponseAccepted),
			outbox.Subject(model.EventContactAccepted),
		},
		AckPolicy:  jetstream.AckExplicitPolicy,
		AckWait:    30 * time.Second,
		MaxDeliver: 10,
	})
	if err != nil {
		return nil, fmt.Errorf("create chat room consumer: %w", err)
	}
	return cons, nil
}

// HandleMessage processes one message body. It is safe to call with duplicates.
func (c *RoomOpenerConsumer) HandleMessage(ctx context.Context, body []byte) error {
	var env outbox.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}
	return c.Service.HandleRoomEvent(ctx, env.ID, env.Type, env.Data)
}

// Run consumes until ctx is cancelled.
func (c *RoomOpenerConsumer) Run(ctx context.Context) error {
	cons, err := c.EnsureConsumer(ctx)
	if err != nil {
		return err
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := c.HandleMessage(ctx, msg.Data()); err != nil {
			c.log().Error("chat room opener", "event", "ChatRoomOpenerFailed", "error", err)
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

func (c *RoomOpenerConsumer) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}
