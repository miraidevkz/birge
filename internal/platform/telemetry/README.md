# platform/telemetry

Traces and metrics for the API and the worker, self-hosted and without personal data ([ADR-0021](../../../docs/adr/0021-observability.md)). Logs stay with `log/slog` (IDs and event names only, AGENTS.md); error reporting (GlitchTip) is not part of this package yet.

| Signal | Where it comes from | Where it goes |
|---|---|---|
| HTTP server spans and `birge_http_*` | `httpserver.Observe` (outermost handler of the API) via `StartHTTP` / `EndHTTP` | OTLP collector / `/metrics` |
| Query spans | `PgxTracer`, installed on every pool by `db.Open` | OTLP collector |
| Publish spans, `birge_outbox_published_total`, `birge_outbox_publish_errors_total` | `outbox.Relay` | OTLP collector / `/metrics` |
| Consume spans, `birge_events_consumed_total` | `outbox.TraceJetStream`, wrapped around the worker's JetStream in wiring | OTLP collector / `/metrics` |
| Outbox lag, due timers, untaken escalations | `Sampler` (worker loop, every 15 s) | `/metrics` |
| SMS guard refusals, store errors, daily budget used | `wiring.guardMetrics` around the guards' counter store | `/metrics` |
| Crisis path errors | `wiring` crisis adapters (`CrisisOpener`, `ChatCrisis`, `LexiconCrisis`) | `/metrics` |
| LLM calls by outcome | `wiring.countingLLM` around the assistant's LLM client | `/metrics` |

Alerts on these metrics: [`deploy/prometheus/alerts.yml`](../../../deploy/prometheus/alerts.yml).

## Design

- **One allow-list, applied in one place.** `NewTracerProvider` puts `NewAllowListProcessor` in front of every exporter, so no span, ours or a library's, reaches the collector without passing it. A span keeps only the keys in `allowlist.go`, and each key only with a value of its shape: UUIDs for IDs, `<context>.<EventName>` for event types, `birge.events.*` subjects, sqlc query names, route templates (no query, no fragment, no segment with a digit, so a raw path with an ID or a phone is dropped), status codes and fixed outcomes. Status descriptions are dropped (error messages quote values: PostgreSQL puts the conflicting value in a unique violation), event attributes and link attributes are filtered the same way, and a span name that is not a route template or one of `db <Query>`, `publish <type>`, `consume <type>`, `HTTP <method>` becomes `span`. The resource has only `service.name`: no host name or process arguments.
- **`db.statement` never exists.** `PgxTracer` names a query by its sqlc name (`-- name: GetMember :one`), the transaction keyword, or `query`; the SQL text and the arguments are never put on the span. It only traces under an existing span, so the worker's polling loops do not create a root span per second.
- **Trace across the broker.** `outbox.Enqueue` stores the caller's span as `trace_parent` when the context passes none (no context had to change). The relay publishes under it, and the consume span of `TraceJetStream` continues it. Consumer handlers get no context from `Consume`, so their own queries are not yet children of the consume span; a consumer can opt in with `ContextWithTraceParent`.
- **HTTP spans are new roots.** A `traceparent` header from the internet is ignored: it would let a client choose trace IDs and force sampling.
- **No tracing without an endpoint.** `Setup` installs the SDK only when `OTEL_EXPORTER_OTLP_ENDPOINT` (or the traces variant) is set; otherwise the global tracer stays the OpenTelemetry no-op and nothing is buffered or sent. The compose stack needs no collector.
- **Metrics in a private registry.** `Registry` holds the Go and process collectors and the Birge metrics only. Labels are bounded sets chosen in code: route template, status, event type, consumer, guard, reason, stage, outcome. Never an ID: that would make every member a time series and put identifiers into the metrics store. `TestMetricLabelsHoldNoIDs` checks label names.
- **Database gauges from the worker.** `Sampler` runs three queries every 15 s on the worker, not the API, so the cost does not grow with API replicas. With several workers each reports the same value; alerts use `max()`. If it fails, `birge_sampler_last_success_timestamp_seconds` stops and `BirgeTelemetrySamplerStale` fires, so a broken sampler cannot silence the crisis alerts.
- **Untaken escalations from the outbox, not from safety.** The platform must not read the safety context's tables. The gauge comes from the safety events in `platform.outbox`: `EscalationOpened` with no later `EscalationTaken` or `EscalationClosed` for the same `escalation_id` (the field the event contracts publish to every consumer). Outbox rows are not deleted today; if outbox retention is added it must keep at least the 7-day lookback. The scan is bounded by the uuidv7 primary key (`uuidV7Floor`), so no new index is needed.
- **SMS daily budget used is per process.** `birge_sms_guard_daily_used` is what this worker counted today (UTC), computed at scrape time so it drops to 0 at midnight; summed over workers it equals the shared budget's count. After a restart a worker starts from 0 for the rest of the day.
- **Crisis path stays independent.** Nothing on the crisis path waits for telemetry: spans are batched in the background, metrics are in-process counters, and exporter or sampler failures are only logged.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `BIRGE_METRICS_ADDR` | unset | internal `/metrics` listener of the API and the worker; its own port, never routed by the ingress |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | unset | OTLP/HTTP collector; unset means no tracing |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG`, `OTEL_SDK_DISABLED`, other `OTEL_EXPORTER_OTLP_*` | SDK defaults | standard OpenTelemetry variables |

## Dependencies

Added for this package, all Apache-2.0, pinned exactly in `go.mod` ([ADR-0012](../../../docs/adr/0012-foss-only-dependencies.md)):

| Module | Version | Why |
|---|---|---|
| `go.opentelemetry.io/otel`, `/trace`, `/sdk` | v1.47.0 | the OpenTelemetry API and SDK the ADR names; vendor-neutral, exports to any self-hosted collector (Tempo) |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` | v1.47.0 | OTLP over HTTP: the standard protocol and variables; no gRPC server or agent needed |
| `github.com/prometheus/client_golang` | v1.25.0 | the reference Prometheus client; the ADR names Prometheus for metrics |

No contrib instrumentation (otelhttp, otelpgx): the handlers here are small, and owning them keeps the attribute set under the allow-list's control. `client_model` (Apache-2.0) is used directly only to read metric values in tests.

## Invariants

- Every exported span has passed `NewAllowListProcessor`; `NewTracerProvider` is the only way this code builds a provider.
- No metric label is an ID, a path, a phone or any text typed by a person.
- `/metrics` is served only on `BIRGE_METRICS_ADDR`.
- Telemetry failures never fail a request, a message or the crisis path.
