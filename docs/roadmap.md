# Roadmap

Work is done in waves; within a wave, at most 2–3 tasks run in parallel. Each task gets a brief (`docs/agents/brief-template.md`). Contracts land before code (ADR-0005): an operation whose contract is merged but whose code is not answers 501 from `internal/domains/<context>/api/pending.go`.

Status as of 2026-10-28.

## Done

- **Foundation:** threat model, `SECURITY.md`/`CONTRIBUTING.md`/`CODE_OF_CONDUCT.md`, Go module with `cmd/birge api|worker|migrate|keygen|kb-reindex|kb-export`, platform packages (db, clock, outbox + JetStream, timers, crypto with Tink, audit, staff OIDC introspection, request validation, object storage, rate limits), OpenAPI 3.1 contract with Go (oapi-codegen strict server) and TypeScript codegen, event schemas, `make migration` and `make db-check` with the destructive-operation gate, boundary test, golangci-lint, CI (`.github/workflows/ci.yml`).
- **Identity and delivery:** auth gateway in front of Kratos (PoW, number allowlist, rate limits, trusted-proxy client IP, daily SMS budget), `membership` (lazy member, children, verification, blocks, reference data, suspension), `notify` (inbox, devices, durable push/SMS delivery, critical SMS fallback, SMS guard, phone resolved at send time), EVENTS stream retention.
- **Safety and AI:** `safety` (L1 lexicon, fixed crisis response, escalations, duty roster), `assistant` (anonymizer, retrieval, LLM provider, L2), `knowledge` (editor, review, expiry, review-due queue, export).
- **Product modules:** `wellbeing`, `circle` + `chat` (rooms, WebSocket delivery, moderation), `emergency` (SOS, district feed, emergency plan, trusted contacts), `sharing`, `directory`, `moderation` (queue, decisions applied in every owning context, block author, legal hold), `privacy` (consents, account deletion in every context, checked by an SQL test over all tables).
- **Clients:** TypeScript API client, mom app (Expo / React Native, kk/ru/en, ADR-0014 and ADR-0025), staff admin web app (escalations, moderation, duty roster, knowledge base; OIDC PKCE).
- **Dev stack:** `deploy/compose` (PostgreSQL, NATS, Kratos, Hydra with a dev login app, web apps), DEMO preset with seeded rooms, specialists and listings, dev SMS relay `cmd/birge-devsms`.
- **Documentation for people:** overview, end-to-end scenarios, staff handbook, content style guide.

## Remaining for the pilot

### Backend
Contract merged, code pending (answers 501):
- [x] Staff list of group rooms with mentors (`staffListRooms`, chat)
- [x] Staff list of verified and suspended members (`staffListMembers`, membership)
- [x] Mentor role: `grantMentorRole` / `revokeMentorRole` (membership) and chat dropping her from rooms on `membership.MentorRoleChanged`
- [x] Staff accounts and the audit log for admins (`listAuditEntries`, `listStaffAccounts`, `getStaffAccount`, platform `staffdir`)
- [ ] Record the concrete staff role in membership and chat audit rows (today `staff`)
- [x] One-time link for external trusted contacts to view a sent emergency plan (`viewExternalPlan`, emergency)
- [x] Metric input events for analytics: `membership.MemberActiveDay`, `circle.TeaRequested`, `assistant.QuestionAnswered`
- [x] Analytics (ADR-0024): wellbeing cohort aggregates, the `analytics` context with `getMetrics` and `getMetricsCsv`
- [x] Rights metrics: `assistant.QuestionAnswered` carries `member_id`; analytics joins `ReplyRated` to it by message
- [ ] The isolation metric needs an isolation item in the check-in (product and psychologist decision)

No contract change needed:
- [x] Push (FCM, APNs) and SMS provider adapters (an SMS provider in Kazakhstan, e.g. SMSC.kz, with a registered sender ID); production refuses the log senders
- [x] Kratos courier hook so sign-in codes go through notify's SMS guard (the dev stack keeps `cmd/birge-devsms`); to verify against a live Kratos
- [ ] More than one API replica: shared store for rate limits and PoW replay, previous KEKs in configuration for rotation, merge the wiring HTTP server back into `platform/httpserver`
- [x] Reference data (cities, districts, needs) for the pilot country, synced by `migrate` (`presets/kz/reference.json`, `BIRGE_REFERENCE_PRESETS`); Kazakh names await native review
- [ ] Role matrix for staff and a deny-by-default test generated from the contract (decided to come after the demo)

### Quality
- [ ] Evaluation runner `cmd/birge-eval` and the AI stage in CI (ADR-0017)
- [ ] Coverage ratchet in CI; tests for the weakest contexts
- [ ] Remaining CI stages: govulncheck, contract diff against released snapshots, images with SBOM and signatures, Helm lint
- [ ] Observability (ADR-0021): OpenTelemetry, metrics, the required alerts
- [ ] ASVS checklist, threat-model review, load test of API and chat gateway

### Deployment
- [ ] Helm charts (k3s): API, worker, migrate job, web apps; CloudNativePG, NATS, Kratos with the phone identity schema and courier hook, Hydra, Garage, TEI, Qdrant, OpenBao, observability
- [ ] Backup and restore drill, key-recovery drill
- [ ] Store builds (phased release); a release keystore for Android

### Clients
- [x] Admin: members, rooms and mentors, richer moderation (filters, block author, legal hold), audit log with staff cards
- [ ] Admin: metrics screen once analytics lands
- [x] Mom app: external plan web page (`/plan`), notification inbox, native push registration (needs a Firebase project for Android delivery)
- [ ] Mom app: success screen after account deletion; screens for a trusted contact (SOS view, invitation, shared plan)
- [ ] Human review of texts: native Kazakh speaker and psychologist (`docs/content-review-2026-10.md`)

### Acceptance
- [ ] Acceptance checklist (`docs/requirements/mvp.md` § 10), pilot

## After the MVP

- [ ] Documentation site built from the Markdown in `docs/` with mdBook (MPL-2.0), translated into kk, ru and en with mdbook-i18n-helpers (Apache-2.0, gettext `.po` files, so the same Weblate workflow as the app strings)
