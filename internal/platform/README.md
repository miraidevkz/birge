# platform

Shared infrastructure used by bounded contexts. Contexts must not import each other; they may import these packages. Generated SQL for platform tables lives in `store/` (do not edit by hand).

| Package | One-liner | ADR |
|---|---|---|
| `auth` | Request-scoped principal (`IdentityID`, staff flag, roles) set by session middleware. | [0008](../../../docs/adr/0008-identity.md) |
| `clock` | Injectable clock so domain code never calls `time.Now` directly. | — |
| `config` | Process env loader (`BIRGE_HTTP_ADDR`, app versions, crisis phones). | [0018](../../../docs/adr/0018-client-compatibility.md) |
| `crypto` | Only path for stored-data encryption: per-member DEK envelope, blind indexes, shredding. | [0009](../../../docs/adr/0009-encryption.md) |
| `db` | PostgreSQL pool open and `InTx` helpers. | [0006](../../../docs/adr/0006-database-and-migrations.md) |
| `httpapi` | RFC 9457 problem+json helpers shared by generated HTTP servers. | [0005](../../../docs/adr/0005-contracts-first.md) |
| `httpserver` | Root mux (`/v1/`, liveness, readiness) and graceful shutdown. | — |
| `meta` | Unauthenticated client config: min/latest app version, crisis phones. | [0018](../../../docs/adr/0018-client-compatibility.md) |
| `outbox` | Transactional outbox enqueue + NATS JetStream relay. | [0007](../../../docs/adr/0007-messaging-outbox-nats.md) |
| `pgtemp` | Throwaway PostgreSQL clusters for tools/tests (dev tooling). | [0006](../../../docs/adr/0006-database-and-migrations.md) |
| `session` | Bearer session validation against Kratos `whoami` with short LRU cache. | [0008](../../../docs/adr/0008-identity.md) |
| `store` | sqlc-generated queries for platform tables (outbox, timers, keys, processed_events). | [0006](../../../docs/adr/0006-database-and-migrations.md) |
| `testinfra` | Test helpers: `Postgres(t)` and `NATS(t)` for integration tests. | — |
| `timers` | Durable PostgreSQL timers claimed with `SKIP LOCKED`. | [0020](../../../docs/adr/0020-scheduler.md) |
