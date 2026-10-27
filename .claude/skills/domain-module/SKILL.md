---
name: domain-module
description: Work inside a bounded context of the Birge Go backend (internal/domains/<context>): add a use case, a port, an adapter, or publish/consume a domain event.
---

# Working in a bounded context

See `docs/architecture.md` (bounded contexts) and ADR-0002, ADR-0007.

## Layout

```
internal/domains/<context>/
  model/      # types and invariants; no I/O
  service/    # use cases; depends on ports only
  port/       # interfaces the service needs (store, clock, publisher, providers)
  adapter/    # Postgres (sqlc), NATS, external providers
  api/        # HTTP handlers implementing the generated OpenAPI interface
  README.md   # what the context owns, its events, its invariants
```

## Rules

- Import only your own context, `internal/platform/...`, and other contexts' exported service interfaces. depguard fails the build otherwise.
- Put authorization checks in `service`, not only in handlers.
- Writing data and publishing an event happen in one transaction through the platform outbox, never "commit, then publish".
- Delays and schedules use platform timers created in the same transaction (ADR-0020); never `time.AfterFunc`, goroutine sleeps or cron.
- Consumers are idempotent: deduplicate by event `id`; repeated delivery must not repeat side effects (no second SMS, no second escalation).
- Encrypted fields go through the platform crypto package with the user's data key. Never decrypt into logs or events.
- Any query that shows members to each other (feeds, matches, rooms, SOS responders) applies the membership block filter (ADR-0019).
- Time comes from the injected clock; never call `time.Now()` in `model` or `service`.

## Tests

- `model` and `service`: unit tests with fake ports, table-driven, including failure paths.
- `adapter`: integration tests against an ephemeral PostgreSQL and NATS.
- Run `go test -race ./internal/domains/<context>/...` and `make check` before reporting.
