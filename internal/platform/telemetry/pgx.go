package telemetry

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// PgxTracer is the pgx query tracer (pgx.QueryTracer) that db.Open installs
// on every pool. A query becomes a child span named "db <QueryName>" with
// only the sqlc query name: never the SQL text, never the arguments
// (db.statement would carry values typed by members).
//
// It traces only queries that run under a traced request or message: the
// worker's polling loops (outbox relay, timers) run several queries a
// second with no parent, and root spans for them would be noise.
type PgxTracer struct{}

var _ pgx.QueryTracer = PgxTracer{}

// pgxSpanKey marks the context returned by TraceQueryStart, so
// TraceQueryEnd ends only a span it started and never the caller's.
type pgxSpanKey struct{}

// TraceQueryStart starts the query span when ctx carries a span.
func (PgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}
	name := QueryName(data.SQL)
	ctx, span := Tracer().Start(ctx, "db "+name, trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String(KeyDBSystem, "postgresql"), attribute.String(KeyDBQueryName, name)))
	return context.WithValue(ctx, pgxSpanKey{}, span)
}

// TraceQueryEnd ends the span TraceQueryStart started, with the SQLSTATE as
// error.type on failure. The error message is not recorded: PostgreSQL
// quotes the offending value in it.
func (PgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(pgxSpanKey{}).(trace.Span)
	if !ok {
		return
	}
	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) {
		span.SetStatus(codes.Error, "")
		span.SetAttributes(attribute.String(KeyErrorType, pgErrorType(data.Err)))
	}
	span.End()
}

func pgErrorType(err error) string {
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr):
		return pgErr.Code
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "error"
	}
}

// QueryName returns the sqlc name of a query ("-- name: GetMember :one" →
// "GetMember"), the keyword of a transaction statement ("begin",
// "commit"), or "query" for anything else. Only this name describes a
// query in telemetry.
func QueryName(sql string) string {
	if rest, ok := strings.CutPrefix(sql, "-- name: "); ok {
		if f := strings.Fields(rest); len(f) > 0 && queryNameRe.MatchString(f[0]) {
			return f[0]
		}
		return "query"
	}
	switch kw := strings.ToLower(strings.TrimRight(strings.TrimSpace(sql), ";")); kw {
	case "begin", "commit", "rollback":
		return kw
	}
	return "query"
}
