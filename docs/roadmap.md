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
- [x] `db/schema.sql` for platform tables (outbox, timers, audit)
- [x] `make migration`, `make db-check` with the destructive-operation gate (`cmd/birge-dbtool`), first migration
- [x] Boundary test (`internal/archtest`), golangci-lint config, `make go-check`
- [ ] Remaining CI stages per `docs/architecture.md` (govulncheck, contracts, migrations, images)
- [ ] `deploy/compose` (dev) and `deploy/helm` skeleton (k3s)
- [ ] Synthetic data generator and demo mode plan

## Wave 1: identity, delivery, clients

- [ ] `membership` + Ory Kratos (SMS code) + SMS provider module with anti-pumping controls
- [ ] `notify` (inbox, push, SMS, escalation ladders) + NATS relay
- [ ] `apps/mobile` and `apps/admin` skeletons (Expo, Expo Router, NativeWind, RN Reusables, i18n kk/ru); low-end Android performance budget measured

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

- [ ] Acceptance checklist (`docs/requirements/mvp.ru.md` § 10), ASVS review, load test (API, chat gateway)
- [ ] Restore drill, key-recovery drill
- [ ] Store builds (phased release), pilot
