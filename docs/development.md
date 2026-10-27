# Development guide

How to build, test and change Birge. Read [`docs/architecture.md`](architecture.md) first for the shape of the system, [`AGENTS.md`](../AGENTS.md) for the rules every change follows, and [`docs/solved-problems.md`](solved-problems.md) for the rules learned from real bugs.

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

## Configuration

`birge` reads its configuration from the environment at startup (`internal/wiring`). It validates everything before it starts, reports every problem at once, and names variables, never their values. Keys are never put in the environment: the variables name files (mounted from the secret store) that hold them. A key file holds the key as text, at least 32 bytes after trailing whitespace is removed: use a text encoding such as the output of `openssl rand -base64 48`, not raw binary (a binary key ending in a whitespace byte would lose it).

"Commands" lists the subcommands that read a variable; the others ignore it.

| Variable | Commands | Default | Meaning |
|---|---|---|---|
| `DATABASE_URL` | all | required | PostgreSQL connection string (it holds a password: treat it as a secret) |
| `BIRGE_APPLY_ROLES` | migrate | `0` | `1` applies `db/roles.sql` after the migrations; the roles must already exist (created by the database operator) |
| `BIRGE_HTTP_ADDR` | api, worker | `:8080` | listen address; the worker serves only `/healthz` and `/readyz` there |
| `BIRGE_MIN_APP_VERSION`, `BIRGE_LATEST_APP_VERSION`, `BIRGE_CRISIS_PHONES` | api | `0.0.0`, the minimum, none | client configuration (`internal/platform/config`, ADR-0018) |
| `KRATOS_PUBLIC_URL` | api | required | cluster-internal Kratos public API: session checks and the `/auth/` gateway |
| `KRATOS_ADMIN_URL` | worker | required | cluster-internal Kratos admin API: phone numbers for SMS (`GET /admin/identities/{id}`, trait `phone`) |
| `NATS_URL` | worker | required | `nats://` or `tls://` URLs, comma-separated |
| `BIRGE_KEK_ID` | api, worker | required | id of the key-encryption key, stored with every wrapped member key; never reuse an id for other key material |
| `BIRGE_KEK_KEYSET_FILE` | api, worker | required | file with the KEK as a cleartext Tink AES256-GCM keyset in JSON (development and first deployments; production moves to OpenBao, ADR-0009) |
| `BIRGE_BLIND_INDEX_KEY_FILE` | api, worker | optional | key for blind indexes; validated when set, not used by any context yet |
| `BIRGE_POW_KEY_FILE` | api | required | key that signs the proof-of-work challenges of the auth gateway |
| `BIRGE_HASH_KEY_FILE` | api | required | key of the hashes of numbers and IPs in gateway logs and limiters; must differ from the PoW key |
| `BIRGE_SMS_ALLOWED_PREFIXES` | api, worker | required | allowed E.164 mobile prefixes, comma-separated (`+7700,+7701`); used by the gateway and the notify SMS guard |
| `BIRGE_AUTH_POW_MAX_NUMBER` | api | `100000` | proof-of-work difficulty |
| `BIRGE_AUTH_RATE_PER_IP`, `BIRGE_AUTH_RATE_PER_NUMBER`, `BIRGE_AUTH_RATE_GLOBAL` | api | `20/1m`, `3/10m`, `20/1s` | gateway token buckets as `<burst>/<interval>`: burst tokens, one more every interval |
| `BIRGE_API_RATE_PER_IP` | api | `300/100ms` | per-IP token bucket for `/v1`, same format; in memory, per replica |
| `BIRGE_TRUSTED_PROXY_HEADER` | api | unset | header set by the ingress (e.g. `X-Forwarded-For`); its right-most value is the client IP. Unset: the connection's address. Set it only behind a proxy that appends to that header |
| `BIRGE_CONSENT_VERSIONS` | api | required | current consent text versions, `<type>=<version>` comma-separated, one for every type (`data_processing`, `ai_crossborder`, `research`) |
| `BIRGE_DELETION_DELAY` | api, worker | `24h` | time from a deletion request to crypto-shredding; at most `24h` |
| `NOTIFY_CRITICAL_SMS_AFTER` | api, worker | `60s` | wait for an acknowledgement before the critical SMS fallback; at most `15m` |
| `BIRGE_SMS_PER_NUMBER_LIMIT`, `BIRGE_SMS_PER_NUMBER_WINDOW` | worker | `5`, `1h` | notify SMS guard: sends per number per window |
| `BIRGE_SMS_GLOBAL_LIMIT`, `BIRGE_SMS_GLOBAL_WINDOW` | worker | `60`, `1m` | notify SMS guard: sends per process per window |
| `BIRGE_SMS_DAILY_BUDGET` | worker | `1000` | notify SMS guard: sends per UTC day (does not block critical SMS) |
| `BIRGE_SMS_TEXTS` | worker | empty in `dev` | neutral fixed SMS bodies, JSON `{"<template>": {"kk": "...", "ru": "...", "en": "..."}}`; required when `BIRGE_DELIVERY_MODE` is not `dev`; an SMS for a template without a text is skipped (and logged) |
| `BIRGE_DELIVERY_MODE` | worker | required | `dev` only: push and SMS are logged (notification ID and channel), not sent. There are no real providers yet, so any other value refuses to start rather than drop notifications |
| `BIRGE_EVENTS_MAX_AGE` | worker | `720h` | JetStream EVENTS stream MaxAge (how long events are retained) |
| `BIRGE_EVENTS_MAX_BYTES` | worker | `1073741824` (1 GiB) | JetStream EVENTS stream MaxBytes (size cap; oldest messages discarded first) |

Health: `GET /healthz` answers 204 while the process runs; `GET /readyz` answers 204 when the database (and, for the worker, NATS) is reachable and 503 otherwise.

## Running locally

Start PostgreSQL, NATS with JetStream and Kratos however you like (Compose files will come with `deploy/compose`). Create local development keys under `secrets/` (git-ignored; never commit keys). Production keys come from the secret store, not from `keygen`:

```sh
go run ./cmd/birge keygen -dir secrets
# writes kek.json, pow.key, hash.key, blind-index.key (mode 0600) and prints BIRGE_KEK_ID=local-1
# refuses to overwrite an existing file
```

Then, with the variables above exported (point `BIRGE_*_KEY_FILE` / `BIRGE_KEK_KEYSET_FILE` at those paths and set `BIRGE_KEK_ID` to the printed id):

```sh
go run ./cmd/birge migrate                       # idempotent; BIRGE_APPLY_ROLES=1 also applies db/roles.sql
go run ./cmd/birge api
BIRGE_DELIVERY_MODE=dev go run ./cmd/birge worker
```

`birge migrate` applies the migrations embedded in the binary (`db/embed.go`), so a deployed binary needs no checkout. Several migration jobs may start at once: a PostgreSQL session lock lets one run at a time.

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
- The crisis path has its own evaluation sets and rules ([ADR-0017](adr/0017-ai-evaluation.md), `.claude/skills/crisis-path/SKILL.md`). The level-1 set is line-based (`evals/datasets/crisis/*.tsv`, see its README); `go test ./internal/domains/safety/crisis/` runs it against the DEMO lexicon and logs recall and the false-positive rate.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `initdb not found in PATH` (test skipped) | install the PostgreSQL 18 server package; on some distributions the binaries live under `/usr/lib/postgresql/18/bin` |
| `make api-check`: file is stale | run `make api-gen` and commit the result |
| `make db-check`: migrations do not match | run `make -s migration name=...` and save the migration |
| lint `exported ... should have comment` | document the identifier (see AGENTS.md § Code documentation) |
