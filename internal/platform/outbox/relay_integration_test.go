package outbox_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/platform/clock"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	"birge/internal/platform/store"
	"birge/internal/platform/testinfra"
)

func TestRelayPublishesOnceAndOnlyCommitted(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	nc, err := nats.Connect(testinfra.NATS(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "EVENTS", Subjects: []string{"birge.events.>"}, Duplicates: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)
	committed := outbox.Event{Type: "safety.EscalationOpened", Version: 1, Data: map[string]string{"escalation_id": "e1"}}
	if err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := outbox.Enqueue(ctx, store.New(tx), now, "", committed)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// A rolled-back transaction must not leave an event behind.
	_ = db.InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := outbox.Enqueue(ctx, store.New(tx), now, "", outbox.Event{Type: "safety.EscalationOpened", Version: 1, Data: map[string]string{"escalation_id": "rolled-back"}}); err != nil {
			t.Fatal(err)
		}
		return context.Canceled
	})

	relay := &outbox.Relay{
		Pool: pool, Publisher: outbox.JetStreamPublisher{JS: js}, Clock: clock.System{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), BatchSize: 10, Interval: time.Second,
	}
	n, err := relay.PublishBatch(ctx)
	if err != nil || n != 1 {
		t.Fatalf("PublishBatch = %d, %v; want 1, nil", n, err)
	}
	if n, err := relay.PublishBatch(ctx); err != nil || n != 0 {
		t.Fatalf("second PublishBatch = %d, %v; want 0, nil", n, err)
	}

	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("stream has %d messages, want 1", info.State.Msgs)
	}
	msg, err := stream.GetMsg(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var env outbox.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != committed.Type || string(env.Data) != `{"escalation_id":"e1"}` {
		t.Errorf("unexpected envelope %s", msg.Data)
	}
	if msg.Header.Get("Nats-Msg-Id") != env.ID.String() {
		t.Errorf("Nats-Msg-Id = %q, want %q", msg.Header.Get("Nats-Msg-Id"), env.ID)
	}
}
