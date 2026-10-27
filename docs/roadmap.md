# Roadmap

Work is done in waves; within a wave, at most 2–3 tasks run in parallel. Each task gets a brief (`docs/agents/brief-template.md`).

## Wave 0: foundation (critical path, done by the tech lead)

- [x] Threat model (`docs/threat-model.md`)
- [ ] ASVS checklist
- [x] `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`
- [x] Go module and `cmd/birge` skeleton with health endpoints and graceful shutdown
- [x] `internal/platform`: db, clock, outbox + JetStream publisher, timers (sqlc)
- [x] Integration test infra without containers (`testinfra`: throwaway PostgreSQL, in-process NATS)
- [ ] `internal/platform`: crypto interface, telemetry
- [x] `contracts/`: OpenAPI 3.1 skeleton, event envelope schema
- [x] Go server codegen (oapi-codegen strict server, one package per area) + `client-config` endpoint
- [ ] TS client codegen (with the apps, on the Mac)
- [x] `db/schema/` for platform tables (outbox, timers, audit)
- [x] `make migration`, `make db-check` with the destructive-operation gate (`cmd/birge-dbtool`), first migration
- [x] Boundary test (`internal/archtest`), golangci-lint config, `make go-check`
- [ ] Remaining CI stages per `docs/architecture.md` (govulncheck, contracts, migrations, images)
- [x] `deploy/compose` (dev)
- [ ] `deploy/helm` skeleton (k3s)
- [ ] Synthetic data generator and demo mode plan

## Wave 1: identity, delivery, clients

- [x] Auth gateway in front of Kratos (PoW, number allowlist, rate limits) and session middleware
- [ ] Wiring: trusted-proxy client IP, API rate limit in front of the session middleware, keys from the secret store
- [ ] Shared store for PoW replay and limiters before running more than one replica; daily SMS budget
- [x] `membership` core (lazy member, children, verification requests, blocks, reference data)
- [x] `notify` core (inbox, devices, durable push/SMS delivery, critical SMS fallback, SMS guard)
- [x] Request validation against the OpenAPI schema
- [x] Wiring: `birge api|worker|migrate` from configuration, embedded migrations, end-to-end tests
- [x] notify: resolve the phone at send time, not at DeliveryRequested time (a Kratos outage must not lose events)
- [x] EVENTS stream retention (MaxAge/MaxBytes); `birge keygen` for local keys
- [ ] previous KEKs in configuration for rotation
- [x] Require SMS texts outside dev mode
- [ ] merge the wiring HTTP server back into platform/httpserver
- [ ] Seed reference data (cities, districts, needs) per country; moderator side of verification
- [ ] Kratos deployment and identity schema (development config in `deploy/compose/kratos`; courier hook to notify still open)
- [ ] `apps/mobile` and `apps/admin` skeletons (Expo, Expo Router, NativeWind, RN Reusables, i18n kk/ru/en); low-end Android performance budget measured

## Wave 2: safety and AI

- [ ] `safety` (L1 lexicon, crisis response, escalations, duty roster)
- [ ] `assistant` (anonymizer, retrieval, LLM provider, L2) + `knowledge` (editor, review, expiry, export)
- [ ] `cmd/birge-eval` with crisis, anonymization and llm-down sets
- [ ] Staff admin: escalation queue, moderation queue

## Wave 3: product modules

- [ ] `wellbeing` (check-ins, schedules, chart, practices)
- [ ] `circle` (tea matching, acquaintances) + `chat` (rooms, delivery, moderation)
- [ ] `emergency` (SOS, district feed, emergency plan, trusted contacts)
- [ ] `sharing` + `directory` + rights FAQ

## Wave 4: acceptance

- [ ] Acceptance checklist (`docs/requirements/mvp.md` § 10), ASVS review, load test (API, chat gateway)
- [ ] Restore drill, key-recovery drill
- [ ] Store builds (phased release), pilot

## Follow-ups from wave 3

- [ ] Wire `emergency` (settings, MemberInfo/Blocks/Acquaintances adapters, notify trusted-contact resolution through `TrustedContactTarget`/`ContactPhone`, district push on `SosShared`)
- [ ] Endpoints for trusted contacts and moderators to read an SOS; list and decline invitations; rate limit invitation notifications
- [ ] One-time links for external contacts to view a sent emergency plan
- [ ] Locale keys and SMS texts for every notification template in use

## Remaining for the MVP (2026-10-27)

### Backend completion
- [x] Wire chat, sharing, directory and S3 object storage
- [x] Wire moderation: content sources per context, queue consumer
- [x] Apply moderation decisions: chat (held and reported messages, legal hold), membership (verification approves members, emits MemberVerified), member suspension for "block author"
- [x] Staff endpoints for mentor assignment, closing reports and legal hold
- [x] Account deletion in every context (consumers of `privacy.DataDeletionRequested`)
- [ ] Analytics: pseudonymous events and the aggregate metrics of requirements § 9 with CSV export
- [ ] Push (FCM/APNs) and SMS provider plugins
- [x] Knowledge: reviewer review-due queue before expiry, export of published content to `knowledge/`

### Quality
- [ ] Evaluation runner `cmd/birge-eval` and the AI stage in CI (ADR-0017)
- [ ] Coverage ratchet in CI; tests for the weakest contexts
- [ ] Remaining CI stages: govulncheck, contract diff against released snapshots, images with SBOM and signatures, Helm lint
- [ ] Observability (ADR-0021): OpenTelemetry, metrics, the required alerts
- [ ] ASVS checklist, threat-model review, load test of API and chat gateway

### Deployment
- [ ] Helm charts (k3s); Compose dev stack done (PostgreSQL, NATS, Kratos, Hydra with a dev login app, web apps): PostgreSQL (CloudNativePG), NATS, Kratos with the phone identity schema and courier hook, Hydra, Garage, TEI, Qdrant, OpenBao, observability
- [ ] Backup and restore drill, key-recovery drill

### Clients
- [x] TypeScript API client from the contract
- [x] Mom app (Expo / React Native, kk/ru/en, design system per ADR-0014); texts need a native-speaker and psychologist review
- [x] Staff admin app: escalations, moderation, duty roster, knowledge base (web, OIDC PKCE); role matrix and the remaining staff screens later

### Documentation for people
- [ ] Overview, end-to-end scenarios with diagrams, moderator handbook

## After the MVP

- [ ] Documentation site built from the Markdown in `docs/` with mdBook (MPL-2.0), translated into kk, ru and en with mdbook-i18n-helpers (Apache-2.0, gettext `.po` files, so the same Weblate workflow as the app strings)
