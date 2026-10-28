package telemetry

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// useGlobalProvider installs an allow-listed in-memory provider as the
// global one (PgxTracer and StartHTTP use the global tracer).
func useGlobalProvider(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := NewTracerProvider(sdktrace.NewSimpleSpanProcessor(exp), "birge-test")
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	return exp
}

func TestPgxTracerNamesQueriesWithoutSQL(t *testing.T) {
	exp := useGlobalProvider(t)
	var tr PgxTracer
	const sql = "-- name: InsertEscalation :one\nINSERT INTO safety.escalations (note) VALUES ($1)"
	args := []any{"I cannot cope, call +77001234567"}

	// No parent span: no query span (polling loops stay quiet).
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: sql, Args: args})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	if n := len(exp.GetSpans()); n != 0 {
		t.Fatalf("%d spans without a parent, want 0", n)
	}

	parentCtx, parent := Tracer().Start(context.Background(), "POST /v1/me/sos")
	ctx = tr.TraceQueryStart(parentCtx, nil, pgx.TraceQueryStartData{SQL: sql, Args: args})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{Code: "23505", Message: "duplicate key (note)=(I cannot cope)"}})
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want the query span only (parent still open)", len(spans))
	}
	q := spans[0]
	if q.Name != "db InsertEscalation" || q.Parent.SpanID() != parent.SpanContext().SpanID() {
		t.Errorf("span %q parent %v", q.Name, q.Parent.SpanID())
	}
	attrs := map[string]string{}
	for _, kv := range q.Attributes {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	want := map[string]string{KeyDBSystem: "postgresql", KeyDBQueryName: "InsertEscalation", KeyErrorType: "23505"}
	if len(attrs) != len(want) {
		t.Errorf("attributes = %v, want exactly %v", attrs, want)
	}
	for k, v := range want {
		if attrs[k] != v {
			t.Errorf("%s = %q, want %q", k, attrs[k], v)
		}
	}
	if q.Status.Code != codes.Error || q.Status.Description != "" {
		t.Errorf("status = %+v", q.Status)
	}
	if !parent.IsRecording() {
		t.Error("TraceQueryEnd ended the caller's span")
	}
	parent.End()
}
