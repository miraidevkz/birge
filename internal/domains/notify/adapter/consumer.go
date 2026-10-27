package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/notify/service"
	"birge/internal/platform/outbox"
)

const (
	streamName    = "EVENTS"
	consumerName  = "notify-delivery-requested"
	filterSubject = "birge.events.notify.DeliveryRequested"
)

// DeliveryConsumer is a JetStream durable consumer for notify.DeliveryRequested.
type DeliveryConsumer struct {
	JS      jetstream.JetStream
	Service *service.Service
	Logger  *slog.Logger
}

// EnsureConsumer creates the durable consumer if needed.
func (c *DeliveryConsumer) EnsureConsumer(ctx context.Context) (jetstream.Consumer, error) {
	cons, err := c.JS.CreateOrUpdateConsumer(ctx, streamName, jetstream.ConsumerConfig{
		Durable:       consumerName,
		FilterSubject: filterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    10,
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer: %w", err)
	}
	return cons, nil
}

// HandleMessage processes one JetStream message. It is safe to call with duplicates.
func (c *DeliveryConsumer) HandleMessage(ctx context.Context, msg jetstream.Msg) error {
	var env outbox.Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}
	if env.Type != "notify.DeliveryRequested" {
		c.log().Info("ignoring event", "event", env.Type, "event_id", env.ID)
		return nil
	}
	req, err := service.ParseDeliveryRequested(env.Data)
	if err != nil {
		return fmt.Errorf("parse DeliveryRequested: %w", err)
	}
	if err := c.Service.HandleDeliveryRequested(ctx, env.ID, req); err != nil {
		return fmt.Errorf("handle DeliveryRequested: %w", err)
	}
	return nil
}

// Run consumes until ctx is cancelled.
func (c *DeliveryConsumer) Run(ctx context.Context) error {
	cons, err := c.EnsureConsumer(ctx)
	if err != nil {
		return fmt.Errorf("ensure consumer: %w", err)
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := c.HandleMessage(ctx, msg); err != nil {
			c.log().Error("delivery requested handler", "error", err)
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

// ConnectJS opens JetStream on url (used by tests and wiring).
func ConnectJS(url string) (*nats.Conn, jetstream.JetStream, error) {
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("jetstream: %w", err)
	}
	return nc, js, nil
}

// EnsureStream creates the events stream used by the outbox relay (tests).
func EnsureStream(ctx context.Context, js jetstream.JetStream) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       streamName,
		Subjects:   []string{"birge.events.>"},
		Duplicates: time.Minute,
	})
	if err != nil {
		return fmt.Errorf("ensure stream: %w", err)
	}
	return nil
}

func (c *DeliveryConsumer) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// PublishDeliveryRequestedTest publishes an envelope for integration tests.
func PublishDeliveryRequestedTest(ctx context.Context, js jetstream.JetStream, eventID uuid.UUID, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	env, err := json.Marshal(outbox.Envelope{
		ID:         eventID,
		Type:       "notify.DeliveryRequested",
		Version:    1,
		OccurredAt: time.Now().UTC(),
		Data:       payload,
	})
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	msg := &nats.Msg{Subject: filterSubject, Data: env, Header: nats.Header{}}
	msg.Header.Set("Content-Type", "application/json")
	if _, err := js.PublishMsg(ctx, msg, jetstream.WithMsgID(eventID.String())); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	return nil
}
