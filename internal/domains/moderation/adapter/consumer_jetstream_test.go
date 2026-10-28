package adapter_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/moderation/adapter"
	"birge/internal/domains/moderation/model"
	"birge/internal/platform/outbox"
	"birge/internal/platform/testinfra"
)

// TestQueueConsumerJetStream: the durable consumer opens items from the
// submission events, drops a redelivered envelope through processed_events
// and terminates a malformed one instead of retrying it forever.
func TestQueueConsumerJetStream(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nc, err := nats.Connect(testinfra.NATS(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: "EVENTS", Subjects: []string{"birge.events.>"}}); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- (&adapter.QueueConsumer{JS: js, Stream: "EVENTS", Service: e.svc}).Run(runCtx) }()
	t.Cleanup(func() { cancel(); <-done })

	publish := func(typ string, body []byte) {
		t.Helper()
		if _, err := js.Publish(ctx, outbox.Subject(typ), body, jetstream.WithMsgID(uuid.NewString())); err != nil {
			t.Fatal(err)
		}
	}
	envelope := func(id uuid.UUID, typ string, data any) []byte {
		raw, _ := json.Marshal(data)
		b, _ := json.Marshal(outbox.Envelope{ID: id, Type: typ, Version: 1, OccurredAt: e.clock.Now(), Data: raw})
		return b
	}
	msg, request := uuid.New(), uuid.New()
	reportEvent := uuid.Must(uuid.NewV7())
	publish(model.EventMessageReported, []byte("not an envelope"))
	publish(model.EventItemSubmitted, envelope(uuid.Must(uuid.NewV7()), model.EventItemSubmitted, model.ItemSubmittedData{ObjectType: "listing", ObjectID: uuid.New()}))
	publish(model.EventMessageReported, envelope(reportEvent, model.EventMessageReported, model.MessageReportedData{MessageID: msg, RoomID: uuid.New(), Reason: "spam"}))
	publish(model.EventMessageReported, envelope(reportEvent, model.EventMessageReported, model.MessageReportedData{MessageID: msg, RoomID: uuid.New(), Reason: "spam"}))
	publish(model.EventVerificationRequested, envelope(uuid.Must(uuid.NewV7()), model.EventVerificationRequested, model.VerificationRequestedData{MemberID: uuid.New(), RequestID: request}))

	settled := func() bool {
		c, err := js.Consumer(ctx, "EVENTS", "moderation-queue")
		if err != nil {
			return false
		}
		ci, err := c.Info(ctx)
		return err == nil && ci.NumAckPending == 0 && ci.NumPending == 0
	}
	for range 200 {
		if e.count(t, `SELECT count(*) FROM moderation.items`) == 2 && settled() {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !settled() {
		t.Fatal("consumer did not settle: a malformed event is being retried")
	}
	if _, n := e.openItem(t, model.KindReport, msg); n != 1 {
		t.Fatalf("report counted %d times for one event", n)
	}
	e.openItem(t, model.KindVerification, request)
	if n := e.count(t, `SELECT count(*) FROM moderation.items`); n != 2 {
		t.Fatalf("items %d, want 2 (the listing object_type is invalid for directory.ItemSubmitted)", n)
	}
}
