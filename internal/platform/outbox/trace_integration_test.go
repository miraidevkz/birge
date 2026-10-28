package outbox_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"birge/internal/platform/clock"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	"birge/internal/platform/store"
	"birge/internal/platform/telemetry"
	"birge/internal/platform/testinfra"
)

// TestTraceCrossesTheBroker checks the whole hop: an event enqueued under a
// request span carries its trace_parent, the relay publishes under it and a
// consumer obtained through TraceJetStream handles it in a span of the
// same trace, with the outcome recorded.
func TestTraceCrossesTheBroker(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := telemetry.NewTracerProvider(sdktrace.NewSimpleSpanProcessor(exp), "birge-test")
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev); _ = tp.Shutdown(context.Background()) })

	ctx := context.Background()
	pool := testinfra.Postgres(t)
	nc, err := nats.Connect(testinfra.NATS(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	raw, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.CreateStream(ctx, jetstream.StreamConfig{Name: "EVENTS", Subjects: []string{"birge.events.>"}, Duplicates: time.Minute}); err != nil {
		t.Fatal(err)
	}
	js := outbox.TraceJetStream(raw)

	reqCtx, req := telemetry.Tracer().Start(ctx, "POST /v1/me/sos")
	if err := db.InTx(reqCtx, pool, func(tx pgx.Tx) error {
		_, err := outbox.Enqueue(reqCtx, store.New(tx), time.Now(), "", outbox.Event{
			Type: "safety.EscalationOpened", Version: 1, Data: map[string]string{"escalation_id": "e1"},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	req.End()

	relay := &outbox.Relay{
		Pool: pool, Publisher: outbox.JetStreamPublisher{JS: js}, Clock: clock.System{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), BatchSize: 10, Interval: time.Second,
	}
	if n, err := relay.PublishBatch(ctx); err != nil || n != 1 {
		t.Fatalf("PublishBatch = %d, %v", n, err)
	}

	cons, err := js.CreateOrUpdateConsumer(ctx, "EVENTS", jetstream.ConsumerConfig{Durable: "trace-test", AckPolicy: jetstream.AckExplicitPolicy})
	if err != nil {
		t.Fatal(err)
	}
	handled := make(chan struct{})
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		_ = msg.Ack()
		close(handled)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Stop()
	select {
	case <-handled:
	case <-time.After(10 * time.Second):
		t.Fatal("message not consumed")
	}

	// The consume span ends right after the handler returns.
	var publish, consume *tracetest.SpanStub
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && consume == nil; time.Sleep(10 * time.Millisecond) {
		for _, s := range exp.GetSpans() {
			switch s.Name {
			case "publish safety.EscalationOpened":
				publish = &s
			case "consume safety.EscalationOpened":
				consume = &s
			}
		}
	}
	if publish == nil || consume == nil {
		t.Fatalf("spans: publish %v consume %v", publish != nil, consume != nil)
	}
	traceID := req.SpanContext().TraceID()
	if publish.SpanContext.TraceID() != traceID || consume.SpanContext.TraceID() != traceID {
		t.Errorf("trace not continued: request %s, publish %s, consume %s",
			traceID, publish.SpanContext.TraceID(), consume.SpanContext.TraceID())
	}
	if publish.SpanKind != trace.SpanKindProducer || consume.SpanKind != trace.SpanKindConsumer {
		t.Errorf("kinds: publish %v consume %v", publish.SpanKind, consume.SpanKind)
	}
	attrs := map[string]string{}
	for _, kv := range consume.Attributes {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	if attrs[telemetry.KeyOutcome] != "ack" || attrs[telemetry.KeyMessagingGroup] != "trace-test" ||
		attrs[telemetry.KeyEventType] != "safety.EscalationOpened" || attrs[telemetry.KeyEventID] == "" {
		t.Errorf("consume attributes = %v", attrs)
	}
}
