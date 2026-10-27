# Architecture

Status: draft, wave 0. Decisions are recorded in [`adr/`](adr/); requirements are in [`requirements/mvp.ru.md`](requirements/mvp.ru.md).

## Principles

1. **Data first.** Losing or corrupting data is worse than downtime. Every write path is designed to survive service degradation.
2. **Safety first.** The crisis path (detect → respond → escalate to a human) never depends on the LLM or on any optional service.
3. **Personal data stays in the deployment's jurisdiction.** Only anonymized text leaves it, and only with explicit consent (the LLM call).
4. **Secure by default.** OWASP ASVS level 2 everywhere, level 3 for the crisis path, personal data and authentication.
5. **FOSS only.** OSI-licensed dependencies; no open-core features or SaaS on the critical path ([ADR-0012](adr/0012-foss-only-dependencies.md)).
6. **Small, explicit modules.** Each module can be built and tested in isolation by one person or one agent from its contract.

## Shape: modular monolith by bounded contexts

One Go binary (`birge`) with subcommands (`api`, `worker`, `migrate`), one PostgreSQL database, one message broker.
Code is split by **bounded context** (domain), not by technical layer ([ADR-0002](adr/0002-modular-monolith.md)).

```
clients (React Native)            backend (Go, one binary)                  infrastructure
┌──────────────┐  HTTPS/OpenAPI  ┌──────────────────────────────┐
│ mom app      │────────────────▶│ api ── domains/* ── platform │──▶ PostgreSQL (CloudNativePG)
│ iOS/Android/ │                 │                    │         │──▶ NATS JetStream
│ web          │                 │ worker ◀── outbox ─┘         │──▶ Qdrant + embeddings (TEI)
└──────────────┘                 └──────────────────────────────┘──▶ OpenBao (keys)
┌──────────────┐  VPN only                    │                 ──▶ Ory Kratos / Hydra
│ staff admin  │──────────────────────────────┘                 ──▶ SMS / push providers (plugins)
│ (web)        │                                                ──▶ S3-compatible storage (Garage)
└──────────────┘                                                ──▶ LLM provider (anonymized text only)
```

### Bounded contexts

| Context | Owns | Type |
|---|---|---|
| `safety` | crisis detection (L1 lexicon), crisis response, escalations, duty roster | core |
| `circle` | tea requests, matching, matches, acquaintances (pairwise "met" status) | core |
| `chat` | rooms, memberships, messages, delivery, chat moderation actions | core |
| `wellbeing` | check-ins (PHQ-4, PSS-4), schedules, practices | core |
| `emergency` | SOS requests, district SOS feed, emergency plan, trusted contacts, childcare policy | core |
| `assistant` | AI conversation pipeline, anonymizer, LLM provider port | supporting |
| `knowledge` | knowledge base documents, chunks, retriever, rights FAQ | supporting |
| `moderation` | moderation queue, reports, decisions | supporting |
| `sharing` | listings, anonymous relay threads | supporting |
| `directory` | specialists, reviews | supporting |
| `membership` | members, roles, verification, children, channel identities | generic |
| `privacy` | consents, account deletion, audit log, retention | generic |
| `notify` | inbox, push, SMS routing, escalation ladders | generic |
| `analytics` | pseudonymized events, aggregates | generic |

Rules:
- A context owns its tables. No other context reads or writes them.
- Contexts talk through **service interfaces** (synchronous, in-process) or **domain events** (asynchronous, via the outbox and NATS).
- Dependency direction: `cmd → domains → platform`. Domains never import each other's internals; `internal/archtest` enforces this on every test run (another context only through its `service` package), and depguard bans unwanted packages.
- Inside a context: `model` (types, invariants) → `service` (use cases) → ports (interfaces) → adapters (Postgres, NATS, providers). Keep it light: no repository-per-entity ceremony.

### Critical flows

| Flow | Path | Guarantee |
|---|---|---|
| Crisis in AI chat | L1 lexicon → anonymize → retrieve → LLM → L2 flag → fixed crisis response + `EscalationOpened` | L1 and the crisis response work with the LLM down |
| Escalation | outbox → NATS → notify: push → SMS → backup after 15 min | at-least-once, idempotent consumers, delivery receipts |
| SOS | client offline queue → API → outbox → moderators + trusted contacts (+ district feed on request) | the client retries until the server acknowledges |
| Account deletion | `DataDeletionRequested` → every context deletes its rows → per-user key destroyed | done in ≤ 24 h; backups become unreadable for that user |

## Repository layout (monorepo)

```
cmd/birge/            # main: api, worker, migrate subcommands
internal/
  domains/<context>/  # one directory per bounded context
  platform/           # db, crypto, events/outbox, timers, config, i18n keys, telemetry
contracts/
  openapi/            # HTTP API (source of truth; server and TS client are generated)
  events/             # JSON Schemas for domain events
db/
  schema.sql          # desired schema (declarative)
  migrations/         # generated, versioned, reviewed migrations
  queries/            # SQL for sqlc
apps/
  mobile/             # mom app (Expo / React Native): iOS, Android, web
  admin/              # staff admin (Expo / react-native-web), separate bundle
packages/             # shared TS: api-client, i18n, ui
knowledge/{kk,ru}/    # published knowledge base, exported from the admin (ADR-0022)
evals/datasets/       # AI regression sets (crisis, anonymization, behavior, false-positive)
deploy/
  helm/               # primary deployment target (Kubernetes / k3s)
  compose/            # alternative for development and small installs
docs/                 # architecture, ADRs, requirements, threat model
```

## Technology

| Concern | Choice | ADR |
|---|---|---|
| Backend | Go, single module | [0003](adr/0003-go-backend.md) |
| Clients | React Native + Expo for the mom app and the admin; App Store and Google Play | [0004](adr/0004-react-native-clients.md) |
| Contracts | OpenAPI 3.1 (HTTP), JSON Schema (events); written before code | [0005](adr/0005-contracts-first.md) |
| Database | PostgreSQL; sqlc for queries; psqldef for diffs, goose to apply; data-safety gates | [0006](adr/0006-database-and-migrations.md) |
| Messaging | NATS JetStream + transactional outbox | [0007](adr/0007-messaging-outbox-nats.md) |
| Identity | Ory Kratos (members, SMS code), Ory Hydra (staff OIDC, WebAuthn); any OIDC IdP pluggable | [0008](adr/0008-identity.md) |
| Encryption | Tink envelope encryption, per-user keys, OpenBao for the master key | [0009](adr/0009-encryption.md) |
| Retrieval | TEI embeddings + Qdrant, versioned by `model_id` | [0010](adr/0010-retrieval.md) |
| Deployment | Kubernetes first (k3s for small installs), Compose as alternative | [0011](adr/0011-kubernetes-first.md) |
| Data residency | personal data in-jurisdiction; LLM gets anonymized text only | [0013](adr/0013-data-residency.md) |
| UI | RN Reusables + NativeWind, Reanimated, Expo Router; accessibility first | [0014](adr/0014-ui-stack.md) |
| Chat | own module: WebSocket + push, PostgreSQL + NATS, server-side moderation, no E2EE | [0016](adr/0016-chat.md) |
| AI evaluation | own Go runner over the real pipeline; deterministic sets per PR, LLM sets on AI changes, nightly, release | [0017](adr/0017-ai-evaluation.md) |
| Client compatibility | additive `/v1`, snapshots of released contracts, 6 months / 3 versions, signed self-hosted OTA | [0018](adr/0018-client-compatibility.md) |
| Member safety | mutual-invisibility blocks across contexts, item-only photos, legal hold for illegal content | [0019](adr/0019-user-safety-and-media-policy.md) |
| Timers | `timers` table, SKIP LOCKED workers, created in the domain transaction | [0020](adr/0020-scheduler.md) |
| Observability | OpenTelemetry → Prometheus, Loki, Tempo, Grafana; GlitchTip; alerts via `notify` | [0021](adr/0021-observability.md) |
| Knowledge base | admin editor with review, per-language publishing, expiry (legal 6 mo, self-help 12 mo), export to repo | [0022](adr/0022-knowledge-base-workflow.md) |
| Object storage | S3 API only; Garage for dev/MVP, local S3-compatible provider in production | [0015](adr/0015-object-storage.md) |

## Replication to other countries

One deployment serves one country. Everything country-specific is configuration or data, never code: languages, crisis phone numbers, district lists, knowledge base, retention periods, phone number prefixes for SMS.

## CI

CI workflows are thin wrappers around `make` targets, so the same checks run locally and can move to another CI (for example Forgejo Actions) without rewriting.

| Stage | Checks |
|---|---|
| Always | gitleaks (secrets), editorconfig |
| Go | gofmt, sqlc diff, golangci-lint (incl. depguard, gosec), go vet, `go test -race` with integration tests and the boundary test, govulncheck |
| Contracts | OpenAPI lint, breaking-change diff against `main` and every released contract in the support window, generated code is up to date |
| Database | migration lint, destructive-operation gate, apply on an ephemeral PostgreSQL, sqlc diff |
| AI | deterministic eval sets; LLM eval sets when prompts, model config, knowledge base or pipeline change |
| Clients | tsc, eslint, unit tests |
| Images | build, SBOM, vulnerability scan, signing |
| Deploy | helm lint, manifest schema validation |

Jobs run only for the paths a change touches. Third-party actions are pinned to commit SHAs.
