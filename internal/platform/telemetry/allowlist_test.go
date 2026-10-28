package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recordSpans returns a tracer whose spans go through the allow-list (the
// same NewTracerProvider chain as production) into an in-memory exporter.
func recordSpans(t *testing.T) (trace.Tracer, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := NewTracerProvider(sdktrace.NewSimpleSpanProcessor(exp), "birge-test")
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return tp.Tracer("test"), exp
}

func TestAllowListStripsForbiddenAttributes(t *testing.T) {
	tracer, exp := recordSpans(t)
	const (
		eventID = "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
		phone   = "+77001234567"
	)
	_, span := tracer.Start(context.Background(), "GET /v1/me/children/{childId}")
	span.SetAttributes(
		// Allowed.
		attribute.String(KeyHTTPRoute, "GET /v1/me/children/{childId}"),
		attribute.Int(KeyHTTPStatus, 200),
		attribute.String(KeyEventID, eventID),
		attribute.String(KeyEventType, "safety.EscalationOpened"),
		attribute.String(KeyDBQueryName, "GetMember"),
		// Forbidden keys.
		attribute.String("user.phone", phone),
		attribute.String("http.url", "https://app.example.invalid/v1/external/plans/view?token=secret-token"),
		attribute.String("url.full", "https://app.example.invalid/plan#secret-token"),
		attribute.String("db.statement", "SELECT * FROM membership.members WHERE phone = '"+phone+"'"),
		attribute.String("db.query.text", "UPDATE x SET note = 'I feel'"),
		attribute.String("enduser.id", "Aigerim"),
		attribute.String("http.request.header.authorization", "Bearer secret-token"),
		// Allowed keys with a value of the wrong shape.
		attribute.String(KeyHTTPRoute, "GET /v1/me/children/0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"),
		attribute.String(KeyEventID, phone),
		attribute.String(KeyDBQueryName, "SELECT 1 FROM x WHERE phone = '"+phone+"'"),
		attribute.String(KeyMessagingDest, "birge.chat.room."+eventID),
		attribute.String(KeyErrorType, "duplicate key value (phone)=("+phone+")"),
		attribute.Int(KeyHTTPStatus, 77001234567),
	)
	span.AddEvent("exception", trace.WithAttributes(
		attribute.String("exception.message", "member "+phone+" not found"),
		attribute.String(KeyErrorType, "23505"),
	))
	span.AddEvent("sms to " + phone)
	span.RecordError(errors.New("insert failed for " + phone))
	span.SetStatus(codes.Error, "pgx: value "+phone)
	span.End()

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	got := spans[0]
	keys := map[string]string{}
	for _, kv := range got.Attributes {
		keys[string(kv.Key)] = kv.Value.String()
	}
	// SetAttributes keeps the last value per key; the bad values came last,
	// so these keys must be gone, not reverted.
	for _, k := range []string{"user.phone", "http.url", "url.full", "db.statement", "db.query.text", "enduser.id",
		"http.request.header.authorization", KeyHTTPRoute, KeyEventID, KeyDBQueryName, KeyMessagingDest, KeyErrorType, KeyHTTPStatus} {
		if v, ok := keys[k]; ok {
			t.Errorf("attribute %s = %q survived the allow-list", k, v)
		}
	}
	if keys[KeyEventType] != "safety.EscalationOpened" {
		t.Errorf("allowed attribute %s dropped: %v", KeyEventType, keys)
	}
	if got.Status.Code != codes.Error || got.Status.Description != "" {
		t.Errorf("status = %+v, want code kept and description dropped", got.Status)
	}
	dump := got.Name
	for _, e := range got.Events {
		dump += " " + e.Name
		for _, kv := range e.Attributes {
			dump += " " + string(kv.Key) + "=" + kv.Value.String()
		}
	}
	for _, kv := range got.Attributes {
		dump += " " + kv.Value.String()
	}
	if strings.Contains(dump, phone) || strings.Contains(dump, "secret-token") || strings.Contains(dump, "Aigerim") {
		t.Errorf("exported span carries personal data: %s", dump)
	}
	if !strings.Contains(dump, "error.type=23505") {
		t.Errorf("allowed event attribute dropped: %s", dump)
	}
}

func TestAllowListKeepsAllowedValues(t *testing.T) {
	ok := []attribute.KeyValue{
		attribute.String(KeyHTTPMethod, "POST"),
		attribute.String(KeyHTTPRoute, "POST /v1/me/sos"),
		attribute.String(KeyHTTPRoute, "/auth/"),
		attribute.String(KeyHTTPRoute, "GET /v1/staff/metrics.csv"),
		attribute.String(KeyHTTPRoute, "unmatched"),
		attribute.Int(KeyHTTPStatus, 503),
		attribute.String(KeyDBSystem, "postgresql"),
		attribute.String(KeyDBQueryName, "ClaimDueTimer"),
		attribute.String(KeyMessagingSystem, "nats"),
		attribute.String(KeyMessagingOp, "process"),
		attribute.String(KeyMessagingDest, "birge.events.notify.DeliveryRequested"),
		attribute.String(KeyMessagingMsgID, "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"),
		attribute.String(KeyMessagingGroup, "notify-delivery"),
		attribute.String(KeyOutcome, "nak"),
		attribute.String(KeyErrorType, "40001"),
	}
	for _, kv := range ok {
		if !Allowed(kv) {
			t.Errorf("Allowed(%s=%s) = false", kv.Key, kv.Value.String())
		}
	}
	bad := []attribute.KeyValue{
		attribute.String(KeyHTTPMethod, "BREW"),
		attribute.Int(KeyHTTPRoute, 1),
		attribute.String(KeyHTTPRoute, "/v1/me/sos?x=1"),
		attribute.String(KeyHTTPRoute, "GET /v1/trusted/plans/7700123"),
		attribute.String(KeyHTTPRoute, "GET /plan#token"),
		attribute.String(KeyOutcome, "maybe"),
		attribute.String(KeyDBSystem, "mysql"),
		attribute.String("birge.member_name", "Aigerim"),
	}
	for _, kv := range bad {
		if Allowed(kv) {
			t.Errorf("Allowed(%s=%s) = true", kv.Key, kv.Value.String())
		}
	}
}

func TestSpanNameIsFiltered(t *testing.T) {
	tracer, exp := recordSpans(t)
	for _, name := range []string{"db GetMember", "publish safety.EscalationOpened", "POST /v1/me/sos", "lookup +77001234567"} {
		_, span := tracer.Start(context.Background(), name)
		span.End()
	}
	var names []string
	for _, s := range exp.GetSpans() {
		names = append(names, s.Name)
	}
	want := "db GetMember|publish safety.EscalationOpened|POST /v1/me/sos|span"
	if got := strings.Join(names, "|"); got != want {
		t.Errorf("span names = %s, want %s", got, want)
	}
}

func TestQueryName(t *testing.T) {
	tests := map[string]string{
		"-- name: GetMember :one\nSELECT 1":   "GetMember",
		"-- name: 1; DROP TABLE x :one":       "query",
		"begin":                               "begin",
		"COMMIT;":                             "commit",
		"SELECT * FROM x WHERE phone = '+77'": "query",
		"":                                    "query",
	}
	for sql, want := range tests {
		if got := QueryName(sql); got != want {
			t.Errorf("QueryName(%q) = %q, want %q", sql, got, want)
		}
	}
}

func TestTraceParentRoundTrip(t *testing.T) {
	tracer, _ := recordSpans(t)
	if tp := TraceParent(context.Background()); tp != "" {
		t.Fatalf("TraceParent without a span = %q, want empty", tp)
	}
	ctx, span := tracer.Start(context.Background(), "db GetMember")
	defer span.End()
	tp := TraceParent(ctx)
	if !strings.HasPrefix(tp, "00-"+span.SpanContext().TraceID().String()+"-") {
		t.Fatalf("TraceParent = %q", tp)
	}
	remote := trace.SpanContextFromContext(ContextWithTraceParent(context.Background(), tp))
	if remote.TraceID() != span.SpanContext().TraceID() || remote.SpanID() != span.SpanContext().SpanID() {
		t.Errorf("round trip lost the span context: %v", remote)
	}
	if ctx := ContextWithTraceParent(context.Background(), "garbage"); trace.SpanContextFromContext(ctx).IsValid() {
		t.Error("a malformed traceparent produced a span context")
	}
}
