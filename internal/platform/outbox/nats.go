package outbox

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// JetStreamPublisher publishes to NATS JetStream with the event ID as the
// message ID, so the stream drops duplicates inside its window.
type JetStreamPublisher struct {
	JS jetstream.JetStream
}

// Publish implements Publisher.
func (p JetStreamPublisher) Publish(ctx context.Context, m Message) error {
	msg := &nats.Msg{Subject: m.Subject, Data: m.Body, Header: nats.Header{}}
	msg.Header.Set("Content-Type", "application/json")
	if _, err := p.JS.PublishMsg(ctx, msg, jetstream.WithMsgID(m.ID.String())); err != nil {
		return fmt.Errorf("jetstream publish: %w", err)
	}
	return nil
}
