# Development guide

How to build, test and change Birge. Read [`docs/architecture.md`](architecture.md) first for the shape of the system, [`AGENTS.md`](../AGENTS.md) for the rules every change follows, and [`docs/solved-problems.md`](solved-problems.md) for the rules learned from real bugs.

## Prerequisites

| Tool | Version | Why |
|---|---|---|
| Go | the version in `go.mod` | backend, tools |
| PostgreSQL server binaries (`initdb`, `postgres` on `PATH`) | 18 | integration tests and migration tooling start throwaway clusters; no server needs to run |
| make, git | any recent | |
| Node.js + npm | a current LTS release | mobile app, shared client packages (see [Clients](#clients)) |

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

### Demo presets

A fresh deployment can start with no configuration or reference data files: when the variables below are unset, `birge` uses the embedded DEMO/EXAMPLE values from package `presets` (`presets/demo/`). Each preset in use is logged once at Warn (`event=DemoPreset name=<preset>`); `/readyz` stays healthy. **A real deployment must replace all of them.** The crisis lexicon and questionnaire thresholds come from the psychologist; consent text versions and crisis phones come from the operator; SMS bodies and membership reference data are deployment-specific. Keys and secrets are never presets (`birge keygen` creates local ones).

| Preset (`name=`) | File | Replaces when unset |
|---|---|---|
| `crisis_lexicon` | `presets/demo/crisis/` (`<kk\|ru\|en>.<category>.txt`) | `BIRGE_CRISIS_LEXICON_DIR` |
| `questionnaire` | `presets/demo/questionnaire.json` (EXAMPLE) | `BIRGE_QUESTIONNAIRE_FILE` |
| `sms_texts` | `presets/demo/sms_texts.json` | `BIRGE_SMS_TEXTS` |
| `consent_versions` | `presets/demo/settings.json` | `BIRGE_CONSENT_VERSIONS` |
| `crisis_phones` | `presets/demo/settings.json` | `BIRGE_CRISIS_PHONES` |
| `reference` | `presets/demo/reference.json` | seeded by `birge migrate` only when membership cities, districts and needs are all empty (never overwrites) |
| `knowledge` | `presets/demo/knowledge/articles.json` | seeded by `birge migrate` only when knowledge tables are empty (never overwrites) |
| `assistant_prompt` | `presets/demo/prompts/assistant.v1.md` (DEMO, not approved) | `BIRGE_ASSISTANT_PROMPT_FILE` |

| Variable | Commands | Default | Meaning |
|---|---|---|---|
| `DATABASE_URL` | all | required | PostgreSQL connection string (it holds a password: treat it as a secret) |
| `BIRGE_APPLY_ROLES` | migrate | `0` | `1` applies `db/roles.sql` after the migrations; the roles must already exist (created by the database operator) |
| `BIRGE_HTTP_ADDR` | api, worker | `:8080` | listen address; the worker serves only `/healthz` and `/readyz` there |
| `BIRGE_MIN_APP_VERSION`, `BIRGE_LATEST_APP_VERSION` | api, worker | `0.0.0`, the minimum | client configuration (`internal/platform/config`, ADR-0018) |
| `BIRGE_CRISIS_PHONES` | api, worker | DEMO (`112` / "Emergency services") | crisis lines returned by safety/wellbeing; JSON `[{"number":"…","label":"…"}]`; unset → `presets/demo/settings.json` |
| `KRATOS_PUBLIC_URL` | api | required | cluster-internal Kratos public API: session checks and the `/auth/` gateway |
| `KRATOS_ADMIN_URL` | api, worker | required | cluster-internal Kratos admin API: phone numbers for SMS and for safety contact reveal (`GET /admin/identities/{id}`, trait `phone`) |
| `BIRGE_CRISIS_LEXICON_DIR` | api, worker | DEMO preset | directory of the level-1 crisis lexicon (`<kk\|ru\|en>.<category>.txt`); startup fails if a configured directory does not load or is empty (ADR-0023). Unset → `presets/demo/crisis/` |
| `BIRGE_ESCALATION_BACKUP_AFTER` | api, worker | `15m` | delay before the backup duty shift is alerted for an open escalation; at most `1h` |
| `BIRGE_SOS_AUTO_CLOSE_AFTER` | api, worker | `24h` | how long an open SOS stays open before the worker closes it; at most `168h` (7 days) |
| `BIRGE_STAFF_SMS_LANG` | api, worker | `ru` | language of SMS alerts to on-duty staff phones (`kk` or `ru`) |
| `BIRGE_STAFF_INTROSPECTION_URL` | api | unset | OAuth 2.0 token introspection endpoint (RFC 7662) of the staff OIDC provider (Hydra: `<admin>/admin/oauth2/introspect`; Keycloak: `<base>/realms/<realm>/protocol/openid-connect/token/introspect`). Setting any `BIRGE_STAFF_*` provider variable below turns staff authentication on and makes the required ones mandatory. All unset: every `/v1/staff/*` request gets 503 `staff_auth_not_configured` (never member auth) |
| `BIRGE_STAFF_CLIENT_ID`, `BIRGE_STAFF_CLIENT_SECRET_FILE` | api | required with staff auth | confidential client the backend introspects as (HTTP Basic); the secret is read from the file (trailing whitespace removed), never from the environment |
| `BIRGE_STAFF_ISSUER` | api | required with staff auth | issuer compared with the token's `iss` exactly, trailing slash included |
| `BIRGE_STAFF_AUDIENCE` | api | required with staff auth | must be one of the token's `aud` values or its `client_id` (Keycloak without an audience mapper: the admin app's client ID) |
| `BIRGE_STAFF_ROLES_CLAIM` | api | `roles` | dotted claim path of the role values in the introspection response: `roles`, `realm_access.roles` (Keycloak), `ext.roles` (Hydra) |
| `BIRGE_STAFF_ROLE_MAP` | api | each role to itself | `<idp value>=<role>` comma-separated; roles `psychologist`, `moderator`, `lawyer`, `admin`; unmapped values are ignored |
| `BIRGE_STAFF_MFA_ACR`, `BIRGE_STAFF_MFA_AMR` | api | one required with staff auth | MFA is required on every staff request: `acr` in the first list, or `amr` containing a value of the second (e.g. `mfa,webauthn,hwk`); read at the top level, else under `ext` (Hydra). Without MFA: 403 `mfa_required` |
| `BIRGE_QUESTIONNAIRE_FILE` | api | EXAMPLE preset | path to the wellbeing questionnaire JSON (version, questions with locale keys, reverse-score ids, crisis item, thresholds, feedback). Unset → `presets/demo/questionnaire.json` (thresholds are placeholders for the psychologist) |
| `BIRGE_TIME_ZONE` | api, worker | `Asia/Almaty` | deployment IANA zone for check-in reminder schedules; hosts must ship current tzdata |
| `BIRGE_PRACTICES_BASE_URL` | api | required | in-jurisdiction object-storage base URL for practice audio (`https://…`) |
| `NATS_URL` | api, worker | required | `nats://` or `tls://` URLs, comma-separated; the API uses NATS core for chat room fan-out, the worker uses JetStream for domain events |
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
| `BIRGE_CONSENT_VERSIONS` | api | DEMO (`*-demo-1`) | current consent text versions, `<type>=<version>` comma-separated, one for every type (`data_processing`, `ai_crossborder`, `research`); unset → `presets/demo/settings.json` |
| `BIRGE_DELETION_DELAY` | api, worker | `24h` | time from a deletion request to crypto-shredding; at most `24h` |
| `NOTIFY_CRITICAL_SMS_AFTER` | api, worker | `60s` | wait for an acknowledgement before the critical SMS fallback; at most `15m` |
| `BIRGE_SMS_PER_NUMBER_LIMIT`, `BIRGE_SMS_PER_NUMBER_WINDOW` | worker | `5`, `1h` | notify SMS guard: sends per number per window |
| `BIRGE_SMS_GLOBAL_LIMIT`, `BIRGE_SMS_GLOBAL_WINDOW` | worker | `60`, `1m` | notify SMS guard: sends per process per window |
| `BIRGE_SMS_DAILY_BUDGET` | worker | `1000` | notify SMS guard: sends per UTC day (does not block critical SMS) |
| `BIRGE_SMS_TEXTS` | worker | DEMO preset | neutral fixed SMS bodies, JSON `{"<template>": {"kk": "...", "ru": "...", "en": "..."}}`; unset → `presets/demo/sms_texts.json`; required when `BIRGE_DELIVERY_MODE` is not `dev` and the value is an empty object; an SMS for a template without a text is skipped (and logged) |
| `BIRGE_DELIVERY_MODE` | worker | required | `dev` only: push and SMS are logged (notification ID and channel), not sent. There are no real providers yet, so any other value refuses to start rather than drop notifications |
| `BIRGE_EVENTS_MAX_AGE` | worker | `720h` | JetStream EVENTS stream MaxAge (how long events are retained) |
| `BIRGE_EVENTS_MAX_BYTES` | worker | `1073741824` (1 GiB) | JetStream EVENTS stream MaxBytes (size cap; oldest messages discarded first) |
| `BIRGE_LLM_MODEL` | api | `claude-opus-5-5` | Anthropic model id for the assistant (logged with every reply) |
| `BIRGE_LLM_API_KEY_FILE` | api | unset | path to the Anthropic API key file; unset or unreadable → Warn `LLMDisabled`, every reply `degraded=true`, process still starts |
| `BIRGE_LLM_BASE_URL` | api | SDK default | optional Messages API base URL (tests, in-jurisdiction gateway); SDK env vars are ignored |
| `BIRGE_ASSISTANT_DAILY_LIMIT` | api | `50` | member assistant messages per calendar day in `BIRGE_TIME_ZONE` |
| `BIRGE_ASSISTANT_PROMPT_FILE` | api | DEMO preset | path to the versioned system prompt (version = file stem); unset → `presets/demo/prompts/assistant.v1.md` (not approved) |
| `BIRGE_ASSISTANT_NAMES_FILE` | api | unset | optional common-names list for anonymization (one name per line, `#` comments); empty → only structural patterns (phone, IIN, e-mail, …) |
| `BIRGE_TEI_URL` | api, worker, kb-reindex | unset | self-hosted text-embeddings-inference base URL (`POST {base}/embed`); together with Qdrant and model id enables retrieval (ADR-0010). Any one missing → Warn `RetrievalDisabled`, assistant gets no fragments |
| `BIRGE_TEI_QUERY_PREFIX` | api, worker, kb-reindex | `query: ` | prepended to search queries (e5-style models); set explicitly when the model needs a different or empty prefix |
| `BIRGE_TEI_PASSAGE_PREFIX` | api, worker, kb-reindex | `passage: ` | prepended to indexed chunks (e5-style models) |
| `BIRGE_EMBEDDING_MODEL_ID` | api, worker, kb-reindex | unset | embedding model id; the Qdrant collection name is `{alias}--{model_id with / → _}`. A model change uses a new collection + alias switch (see `internal/domains/knowledge/retrieval` package doc); then `birge kb-reindex` |
| `BIRGE_QDRANT_URL` | api, worker, kb-reindex | unset | Qdrant REST base URL |
| `BIRGE_QDRANT_ALIAS` | api, worker, kb-reindex | `birge-kb` | Qdrant alias Search uses; physical collection includes the model id |
| `BIRGE_QDRANT_SCORE_THRESHOLD` | api, worker, kb-reindex | `0.3` | minimum cosine score for a retrieval hit (0..1) |
| `BIRGE_CHAT_WS_ORIGINS` | api | unset | comma-separated WebSocket `Origin` patterns for `GET /v1/chat/ws` (coder/websocket); unset allows native clients that send no Origin |
| `BIRGE_S3_ENDPOINT` | api, worker | unset | S3-compatible API host (`host:port` or `http(s)://…`) for item photos (ADR-0015; Garage and peers). Unset → Warn `ObjectStorageDisabled`; photo uploads answer 503 `storage_not_configured` |
| `BIRGE_S3_BUCKET` | api, worker | required with S3 | bucket name |
| `BIRGE_S3_REGION` | api, worker | `garage` | S3 region string (any value the provider accepts) |
| `BIRGE_S3_ACCESS_KEY_FILE`, `BIRGE_S3_SECRET_KEY_FILE` | api, worker | required with S3 | paths to the access key and secret (trailing whitespace removed); never put keys in the environment |
| `BIRGE_S3_PUBLIC_BASE_URL` | api, worker | required with S3 | public origin for processed photo URLs (`https://objects.example.invalid`); `PresignGet` returns `{base}/{key}` |

`birge kb-reindex` rebuilds the current model's Qdrant collection from every published, non-expired knowledge fragment. It requires `DATABASE_URL`, `BIRGE_TEI_URL`, `BIRGE_QDRANT_URL` and `BIRGE_EMBEDDING_MODEL_ID`.

`birge kb-export --out <dir>` writes every current published knowledge version to `<dir>/{kk,ru,en}/<topic-code>/<article-slug>.md` with YAML front matter (`title`, `topic`, `source_url`, `reviewed_by` = reviewer role, `reviewed_at`, `expires_at`). Drafts and expired rights are skipped; `.md` files under `<dir>/{kk,ru,en}/<topic>/` that the run did not write (retired or expired versions) are removed, nothing else under `<dir>` is touched. Output is deterministic so a rerun without content changes produces no diff. Requires `DATABASE_URL` only (migrate + seed first). Convenience target:

```sh
make kb-export   # writes under knowledge/
```

Health: `GET /healthz` answers 204 while the process runs; `GET /readyz` answers 204 when the database (and NATS for the API and the worker) is reachable and 503 otherwise.

## Running locally

The quickest way is the development stack in `deploy/compose` (Docker or Colima with the Compose plugin):

```sh
deploy/compose/up.sh          # PostgreSQL, NATS, Kratos v26 (phone + SMS code), birge migrate, api, worker
deploy/compose/up.sh web      # web client on http://localhost:8081 (needs Node and npm ci)
deploy/compose/up.sh codes    # latest SMS texts Kratos queued: there is no SMS provider in development
deploy/compose/up.sh down     # stop; data volumes stay
```

It publishes only the API on `127.0.0.1:8080` (the auth gateway at `/auth/`). Keys and the Kratos secrets are generated once into a volume; push and SMS are logged, not sent. A phone on USB reaches it with `adb reverse tcp:8080 tcp:8080`, which matches the app's default URLs. Android release builds refuse plain HTTP: build the APK with `BIRGE_DEV_CLEARTEXT=1` to allow it (development only). `deploy/compose/up.sh web` exports the web client against the stack and serves it with the API on `http://localhost:8081`; the sign-in proof-of-work needs WebCrypto, which browsers allow only in a secure context, so from another machine open it through `ssh -L 8081:localhost:8081 <host>`. Allowed sign-in prefixes default to `+77`; override with `BIRGE_SMS_ALLOWED_PREFIXES`.

Sign-in limits are looser than in production so testers can retry from one number (60 code requests per number, 500 SMS per number per hour, 30-minute flows); set `BIRGE_AUTH_RATE_PER_NUMBER`, `BIRGE_AUTH_RATE_PER_IP` or `BIRGE_SMS_PER_NUMBER_LIMIT` to test the real ones.

Staff sign in through a development Ory Hydra (issuer `http://localhost:4444/`) and `cmd/birge-devlogin` on `http://localhost:3000`, which lists one fixed account per staff role and signs in without a password. Its tokens carry `amr` `dev` in place of a second factor, and only this stack's API accepts that (`BIRGE_STAFF_MFA_AMR=dev`); never deploy it. The admin app is the public client `birge-admin` (authorization code with PKCE, redirect `http://localhost:8082/`, see `deploy/compose/hydra/admin-client.json`). From another machine forward ports 4444, 3000 and 8082 as well.

To show the member app outside the machine, publish only port 8081 through an HTTPS tunnel or reverse proxy, then run `BIRGE_PUBLIC_URL=https://<host> BIRGE_CHAT_WS_ORIGINS=<host> deploy/compose/up.sh web` and build the APK with `EXPO_PUBLIC_API_URL=https://<host>/v1 EXPO_PUBLIC_KRATOS_URL=https://<host>/auth`. Never publish the admin (8082), Hydra (4444) or the development login app (3000): the login app signs anyone in as any staff role. A tunnel that terminates TLS outside the deployment's jurisdiction is for synthetic demo data only (non-negotiable 3).

To run the binaries yourself instead, start PostgreSQL, NATS with JetStream and Kratos however you like. Create local development keys under `secrets/` (git-ignored; never commit keys). Production keys come from the secret store, not from `keygen`:

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

1. Edit your context's `db/schema/NNNN_<context>.sql`.
2. Add or change queries in `db/queries/<context>_*.sql`, run `make sqlc`.
3. `make -s migration name=<what_changed>` and save the output under the printed file name in `db/migrations/`.
4. `make db-check`. Destructive changes are refused unless approved through an ADR ([ADR-0006](adr/0006-database-and-migrations.md), `.claude/skills/db-migration/SKILL.md`).

### Add a bounded context

1. Write the contract (OpenAPI tag + `contracts/codegen/<context>.yaml` + a line in `API_AREAS` in the `Makefile`) and the event schemas.
2. Create `db/schema/NNNN_<context>.sql` and a block in `sqlc.yaml` scoped to that file.
3. Lay out `model/`, `service/`, `port/`, `adapter/`, `api/` (`.claude/skills/domain-module/SKILL.md`), and a `README.md` from [`docs/templates/context-readme.md`](templates/context-readme.md).
4. `internal/archtest` enforces the boundaries on every test run.
5. If the context stores anything about a member, add account deletion (below); otherwise `TestAccountDeletionEveryContext` fails on the new table.

### Account deletion in a context

`privacy.DataDeletionRequested` is consumed once per context by `wiring.DeletionConsumer` (durable `<context>-deletion`, dedup consumer `<context>.Deletion` in `platform.processed_events`), which calls the context's `service.Deletion.DeleteMember`. A context adds:

- `port.MemberEraser` and an adapter `EraseMember` that runs in **one transaction**: delete her own rows; where another member's record refers to her, clear content and keep a tombstone; skip legal-hold items (log `DeletionSkippedLegalHold` with the record ID); cancel her pending timers (`timers.CancelSubjects`); write `audit.RecordMemberDeleted(ctx, q, "<context>", memberID)` (once, actor system).
- Side effects outside the database (object storage, the identity provider) go into a timer created in that transaction (`sharing.DeleteObjects`, `membership.DeleteIdentity`).
- It must be idempotent: a repeated run changes no row.
- Its tables and member-like columns in `deletionPolicy` (`internal/wiring/deletion_e2e_test.go`), and the rules in its README.

The privacy timer `privacy.DeletionDue` shreds the member key at `BIRGE_DELETION_DELAY` (default 24 h) and then deletes the consent history.

### Publish an event or schedule work

- Events: `outbox.Enqueue` with the queries bound to your transaction; never publish after commit ([ADR-0007](adr/0007-messaging-outbox-nats.md)).
- Delays and schedules: `timers.Schedule` in the same transaction; the handler has the `timers.Handler` signature ([ADR-0020](adr/0020-scheduler.md)).

## Testing

- Unit tests use fakes of the context's ports (test-only packages such as `notifytest`).
- Integration tests use `internal/platform/testinfra`: `testinfra.Postgres(t)` (throwaway cluster with the schema applied) and `testinfra.NATS(t)` (in-process JetStream).
- Wiring end-to-end tests in `internal/wiring` exercise the API and worker together (including moderation: listing/held/report/verification decisions over JetStream).
- The crisis path has its own evaluation sets and rules ([ADR-0017](adr/0017-ai-evaluation.md), `.claude/skills/crisis-path/SKILL.md`). The level-1 set is line-based (`evals/datasets/crisis/*.tsv`, see its README); `go test ./internal/domains/safety/crisis/` runs it against the DEMO lexicon and logs recall and the false-positive rate.

## Clients

The clients are npm workspaces at the repository root: `apps/mobile` (Expo Router app for iOS, Android and web) and the shared packages `packages/ui` (tokens and components), `packages/api` (generated API client, outbox, sign-in) and `packages/i18n` (locale files). Each has a README; start with [`apps/mobile/README.md`](../apps/mobile/README.md).

```sh
npm ci              # once; installs all workspaces
npm run check       # typecheck, lint (ESLint + Prettier), tests, api-check, web-build; same as `make app-check`
npm test -- packages/ui        # one workspace's tests
npm run format      # Prettier --write
```

Rules the setup enforces:

- Exact versions only (`.npmrc` `save-exact`). Expo SDK packages are added with `npx expo install <pkg>` from `apps/mobile` so their versions match the SDK, then pinned exactly.
- No install scripts (`.npmrc` `ignore-scripts`): a dependency that only works with its postinstall script is not accepted.
- Expo telemetry is off in every script (`EXPO_NO_TELEMETRY=1`).

### Running npm on a build host

If Node is not installed on your machine, run the same npm commands on a build host that has it: sync the working tree there (tracked and untracked files, without `node_modules`), run `npm ci` and the command, and copy back only the files the command changed (for `npm install`, `package-lock.json`). Keep install scripts disabled on that host as well. Files that a command creates (a new generated file) only come back if your sync includes them, so create the file first or add it with `git add -N`.

### Regenerate the API client

After a change to `contracts/openapi/birge.yaml`:

```sh
npm run api-gen     # writes packages/api/src/schema.gen.ts
npm run api-check   # must pass; it also runs inside npm test
```

Commit the regenerated file with the contract change. A type error in the app after regeneration means a screen relied on something the contract changed.

### Add a locale key

Add the key at the same path to `packages/i18n/src/locales/{kk,ru,en}/<namespace>.json`, then use `t('<path>')` from `useTranslation('<namespace>')`. The locale test fails if one language misses it, a value is empty, or the `{{placeholders}}` differ. Details: [`packages/i18n/README.md`](../packages/i18n/README.md).

### Add a UI component

Check `packages/ui` first, then RN Reusables; a pattern used on two screens becomes a component. Use tokens only, own accessibility and reduce-motion, export it from `packages/ui/src/index.ts`, document it in [`packages/ui/README.md`](../packages/ui/README.md) and add a test. Screens may not import raw React Native primitives or pass `style`; ESLint reports it.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `initdb not found in PATH` (test skipped) | install the PostgreSQL 18 server package; on some distributions the binaries live under `/usr/lib/postgresql/18/bin` |
| `make api-check`: file is stale | run `make api-gen` and commit the result |
| `make db-check`: migrations do not match | run `make -s migration name=...` and save the migration |
| lint `exported ... should have comment` | document the identifier (see AGENTS.md § Code documentation) |
| `npm run api-check`: schema.gen.ts is stale | run `npm run api-gen` and commit the result |
| Jest: `render` function has not been called | Testing Library 14 renders asynchronously: `await render(...)`, `await fireEvent.press(...)` |
| `expo export` rewrites `apps/mobile/tsconfig.json` | NativeWind adds `nativewind-env.d.ts` when it is missing at the app root; keep that file |
