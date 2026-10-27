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
- [ ] `deploy/compose` (dev) and `deploy/helm` skeleton (k3s)
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
- [ ] Kratos deployment and identity schema
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

## After the MVP

- [ ] Documentation site built from the Markdown in `docs/` with mdBook (MPL-2.0), translated into kk, ru and en with mdbook-i18n-helpers (Apache-2.0, gettext `.po` files, so the same Weblate workflow as the app strings)
