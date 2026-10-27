# Development guide

How to build, test and change Birge. Read [`docs/architecture.md`](architecture.md) first for the shape of the system and [`AGENTS.md`](../AGENTS.md) for the rules every change follows.

## Prerequisites

| Tool | Version | Why |
|---|---|---|
| Go | the version in `go.mod` | backend, tools |
| PostgreSQL server binaries (`initdb`, `postgres` on `PATH`) | 18 | integration tests and migration tooling start throwaway clusters; no server needs to run |
| make, git | any recent | |
| Node.js + pnpm | see `apps/` (when added) | mobile app and admin |

No Docker is needed for development or tests. Development tools (sqlc, oapi-codegen, golangci-lint, psqldef) are pinned in `tools/go.mod` and run with `go tool`; you do not install them.

## Everyday commands

| Command | What it does |
|---|---|
| `make go-check` | everything CI runs for Go: formatting, generated code up to date, migrations match the schema, lint, vet, race tests, build |
| `go test ./internal/domains/<context>/...` | tests of one context (integration tests start their own PostgreSQL and NATS) |
| `make api-gen` | regenerate HTTP server code from `contracts/openapi/birge.yaml` |
| `make sqlc` | regenerate typed queries from `db/queries/*.sql` |
| `make -s migration name=<snake_case>` | print the migration that brings the database to `db/schema/*.sql` |
| `make db-check` | verify migrations: safety gate, apply to a fresh database, compare with the schema |
| `make lint` | golangci-lint with the project config |

Set `BIRGE_REQUIRE_INTEGRATION=1` to make integration tests fail instead of skip when PostgreSQL binaries are missing (CI does this).

## Repository layout

See [architecture § Repository layout](architecture.md#repository-layout-monorepo). In short: `cmd/` binaries, `internal/domains/<context>/` bounded contexts, `internal/platform/` shared infrastructure, `contracts/` API and event contracts, `db/` schema, queries and migrations.

## Workflows

### Change the HTTP API

1. Edit `contracts/openapi/birge.yaml` (see `.claude/skills/api-contract/SKILL.md`; it applies to people too).
2. `make api-gen`, then implement the new methods of the generated `StrictServerInterface` in `internal/domains/<context>/api/`.
3. Additive changes only within `/v1` ([ADR-0018](adr/0018-client-compatibility.md)).

### Change the database

1. Edit your context's `db/schema/NNN_<context>.sql`.
2. Add or change queries in `db/queries/<context>_*.sql`, run `make sqlc`.
3. `make -s migration name=<what_changed>` and save the output under the printed file name in `db/migrations/`.
4. `make db-check`. Destructive changes are refused unless approved through an ADR ([ADR-0006](adr/0006-database-and-migrations.md), `.claude/skills/db-migration/SKILL.md`).

### Add a bounded context

1. Write the contract (OpenAPI tag + `contracts/codegen/<context>.yaml` + a line in `API_AREAS` in the `Makefile`) and the event schemas.
2. Create `db/schema/NNN_<context>.sql` and a block in `sqlc.yaml` scoped to that file.
3. Lay out `model/`, `service/`, `port/`, `adapter/`, `api/` (`.claude/skills/domain-module/SKILL.md`), and a `README.md` from [`docs/templates/context-readme.md`](templates/context-readme.md).
4. `internal/archtest` enforces the boundaries on every test run.

### Publish an event or schedule work

- Events: `outbox.Enqueue` with the queries bound to your transaction; never publish after commit ([ADR-0007](adr/0007-messaging-outbox-nats.md)).
- Delays and schedules: `timers.Schedule` in the same transaction; the handler has the `timers.Handler` signature ([ADR-0020](adr/0020-scheduler.md)).

## Testing

- Unit tests use fakes of the context's ports (test-only packages such as `notifytest`).
- Integration tests use `internal/platform/testinfra`: `testinfra.Postgres(t)` (throwaway cluster with the schema applied) and `testinfra.NATS(t)` (in-process JetStream).
- The crisis path has its own evaluation sets and rules ([ADR-0017](adr/0017-ai-evaluation.md), `.claude/skills/crisis-path/SKILL.md`).

## Troubleshooting

| Symptom | Cause |
|---|---|
| `initdb not found in PATH` (test skipped) | install the PostgreSQL 18 server package; on some distributions the binaries live under `/usr/lib/postgresql/18/bin` |
| `make api-check`: file is stale | run `make api-gen` and commit the result |
| `make db-check`: migrations do not match | run `make -s migration name=...` and save the migration |
| lint `exported ... should have comment` | document the identifier (see AGENTS.md § Code documentation) |
