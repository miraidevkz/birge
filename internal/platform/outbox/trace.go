package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"birge/internal/platform/telemetry"
)

// TraceJetStream wraps js so that every consumer obtained through it
// (CreateOrUpdateConsumer, CreateConsumer, Consumer) handles each message
// of Consume inside a consumer span and counts it in
// birge_events_consumed_total by consumer, event type and outcome.
//
// The span's parent is the envelope's trace_parent, so a check-in, the
// escalation it opens and the SMS the worker sends end up in one trace.
// The span carries only the event ID and type, the subject and the
// consumer name; the payload is never read beyond the envelope header.
//
// Consumers keep their own code: the decorator sits in wiring, between the
// connection and every context's consumer. Their handlers receive no
// context from Consume, so their database work is not yet a child of the
// consume span; a consumer that wants that can start from
// telemetry.ContextWithTraceParent itself.
func TraceJetStream(js jetstream.JetStream) jetstream.JetStream {
	return tracedJetStream{JetStream: js}
}

type tracedJetStream struct {
	jetstream.JetStream
}

// CreateOrUpdateConsumer returns the consumer with traced Consume.
func (t tracedJetStream) CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	c, err := t.JetStream.CreateOrUpdateConsumer(ctx, stream, cfg)
	if err != nil {
		return nil, err //nolint:wrapcheck // a decorator passes the error through unchanged
	}
	return tracedConsumer{Consumer: c, name: consumerLabel(cfg.Durable, cfg.Name)}, nil
}

// CreateConsumer returns the consumer with traced Consume.
func (t tracedJetStream) CreateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	c, err := t.JetStream.CreateConsumer(ctx, stream, cfg)
	if err != nil {
		return nil, err //nolint:wrapcheck // a decorator passes the error through unchanged
	}
	return tracedConsumer{Consumer: c, name: consumerLabel(cfg.Durable, cfg.Name)}, nil
}

// Consumer returns the existing consumer with traced Consume.
func (t tracedJetStream) Consumer(ctx context.Context, stream, name string) (jetstream.Consumer, error) {
	c, err := t.JetStream.Consumer(ctx, stream, name)
	if err != nil {
		return nil, err //nolint:wrapcheck // a decorator passes the error through unchanged
	}
	return tracedConsumer{Consumer: c, name: consumerLabel(name, "")}, nil
}

// consumerLabel is the durable name, set in code by each consumer; an
// ephemeral consumer has none and is reported as "ephemeral".
func consumerLabel(durable, name string) string {
	switch {
	case durable != "":
		return durable
	case name != "":
		return name
	default:
		return "ephemeral"
	}
}

type tracedConsumer struct {
	jetstream.Consumer
	name string
}

// Consume wraps handler with a span and the consumed-events counter.
func (c tracedConsumer) Consume(handler jetstream.MessageHandler, opts ...jetstream.PullConsumeOpt) (jetstream.ConsumeContext, error) {
	return c.Consumer.Consume(func(msg jetstream.Msg) { c.handle(handler, msg) }, opts...) //nolint:wrapcheck // passed through unchanged
}

// envelopeHeader is the part of the envelope the span needs; Data is not
// decoded.
type envelopeHeader struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	TraceParent string `json:"trace_parent"`
}

func (c tracedConsumer) handle(handler jetstream.MessageHandler, msg jetstream.Msg) {
	start := time.Now()
	var head envelopeHeader
	// A malformed body is the handler's to reject; the span then has no
	// event attributes.
	_ = json.Unmarshal(msg.Data(), &head)
	eventType := "unknown"
	if eventTypeRe.MatchString(head.Type) {
		eventType = head.Type
	}
	ctx := telemetry.ContextWithTraceParent(context.Background(), head.TraceParent)
	_, span := telemetry.Tracer().Start(ctx, "consume "+eventType, trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithTimestamp(start),
		trace.WithAttributes(
			attribute.String(telemetry.KeyMessagingSystem, "nats"),
			attribute.String(telemetry.KeyMessagingOp, "process"),
			attribute.String(telemetry.KeyMessagingDest, msg.Subject()),
			attribute.String(telemetry.KeyMessagingGroup, c.name),
			attribute.String(telemetry.KeyEventType, eventType),
			attribute.String(telemetry.KeyEventID, head.ID),
		))
	tm := &tracedMsg{Msg: msg, outcome: "unacked"}
	handler(tm)
	span.SetAttributes(attribute.String(telemetry.KeyOutcome, tm.outcome))
	if tm.outcome == "nak" || tm.outcome == "term" {
		span.SetStatus(codes.Error, "")
	}
	span.End()
	telemetry.EventsConsumed.WithLabelValues(c.name, eventType, tm.outcome).Inc()
}

// tracedMsg records how the handler settled the message. A handler runs
// on one goroutine per message, so the field needs no lock.
type tracedMsg struct {
	jetstream.Msg
	outcome string
}

// Ack records "ack" and acknowledges.
func (m *tracedMsg) Ack() error {
	m.outcome = "ack"
	return m.Msg.Ack() //nolint:wrapcheck // passed through unchanged
}

// DoubleAck records "ack" and acknowledges with confirmation.
func (m *tracedMsg) DoubleAck(ctx context.Context) error {
	m.outcome = "ack"
	return m.Msg.DoubleAck(ctx) //nolint:wrapcheck // passed through unchanged
}

// Nak records "nak" and asks for redelivery.
func (m *tracedMsg) Nak() error {
	m.outcome = "nak"
	return m.Msg.Nak() //nolint:wrapcheck // passed through unchanged
}

// NakWithDelay records "nak" and asks for redelivery after delay.
func (m *tracedMsg) NakWithDelay(delay time.Duration) error {
	m.outcome = "nak"
	return m.Msg.NakWithDelay(delay) //nolint:wrapcheck // passed through unchanged
}

// Term records "term" and stops redelivery.
func (m *tracedMsg) Term() error {
	m.outcome = "term"
	return m.Msg.Term() //nolint:wrapcheck // passed through unchanged
}

// TermWithReason records "term" and stops redelivery with a reason.
func (m *tracedMsg) TermWithReason(reason string) error {
	m.outcome = "term"
	return m.Msg.TermWithReason(reason) //nolint:wrapcheck // passed through unchanged
}
