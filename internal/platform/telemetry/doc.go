// Package telemetry is the platform's traces and metrics (ADR-0021).
//
// It owns:
//   - the tracer provider (Setup): OpenTelemetry, exported over OTLP/HTTP to
//     a self-hosted collector configured by the standard
//     OTEL_EXPORTER_OTLP_* variables; with no endpoint it stays the no-op
//     tracer and nothing is exported;
//   - the attribute allow-list (Allowed, NewAllowListProcessor), the single
//     place that decides what a span may carry. Every exported span passes
//     through it: IDs (UUIDs), event names, route templates, sqlc query
//     names, status codes and fixed outcomes. Everything else (message
//     text, names, phones, tokens, URLs, SQL text and arguments, error
//     messages) is dropped;
//   - the spans helpers for HTTP (StartHTTP, EndHTTP), PostgreSQL
//     (PgxTracer) and the outbox trace_parent (TraceParent,
//     ContextWithTraceParent);
//   - the Prometheus registry and every Birge metric (Registry, Handler),
//     and the Sampler that refreshes the gauges read from the database.
//
// Invariants: no metric label holds an ID or anything typed by a person;
// /metrics is served only on the internal listener (BIRGE_METRICS_ADDR);
// the crisis path never depends on telemetry (a failing exporter or
// sampler only logs).
package telemetry
