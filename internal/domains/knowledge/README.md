# knowledge

Reviewed knowledge base for «Мои права» and self-help: topics, versioned articles (kk / ru / en), draft → review → publish with role checks, expiry timers and DEMO seed. Serves `docs/requirements/mvp.md` § 4.7 and § 5 (Knowledge base); ADR-0010 (retrieval indexes only current published versions), ADR-0020 (timers), ADR-0022 (workflow).

## Owns

| Table (schema `knowledge`) | Purpose | Personal data |
|---|---|---|
| `topics` | Browse topics (`rights` / `selfhelp`), unique `code`, titles per language | none |
| `articles` | One question under a topic; kind denormalised from the topic | none |
| `versions` | One language version: `draft → in_review → published → retired`; title, body, optional `source_url`; author / reviewer staff ids; `expires_at`; `needs_sync`; Idempotency-Key | staff ids only (not personal data of members) |

Writes to `platform.audit_log` through the `Auditor` port. At most one `published` version per `(article_id, lang)` (partial unique index). At most one open `draft`/`in_review` per `(article_id, lang)`.

## API

Operations of tag `knowledge` in `contracts/openapi/birge.yaml`.

| Operation | Rules not in the contract |
|---|---|
| `GET /knowledge/topics` | Member session. Topics of `kind` with ≥ 1 published version in `lang`. |
| `GET /knowledge/topics/{id}/articles` | Member. Summaries carry `current` or `being_rechecked` (rights past `expires_at`). |
| `GET /knowledge/articles/{id}` | Member. Rights answers set `disclaimerKey=knowledge.lawyer_disclaimer`. Expired rights → `being_rechecked`; self-help stays `current` until replaced. |
| `GET/POST /staff/knowledge/topics` | Staff. Create: `admin` any kind; `psychologist` self-help; `lawyer` rights. |
| `GET/POST /staff/knowledge/articles` | Staff. Create draft: matching author role; Idempotency-Key per author. |
| `PUT …/versions/{id}` | Author only, while `draft`. |
| `POST …/submit` | Author only, `draft → in_review`. |
| `POST …/publish` | Matching reviewer role, not the author; rights require `sourceUrl`; retires previous published of same article+lang; marks other languages `needs_sync`; schedules timers; audit; `knowledge.ArticlePublished`. |
| `POST …/reject` | Matching reviewer role, not the author; reason code; back to `draft`. |

Handler wiring into `birge api` is out of scope for this brief; the seeding hook is wired in `birge migrate`.

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `knowledge.ArticlePublished` v1 | publish (not on replay) | outbox, same transaction as version + timers + audit |
| publishes | `knowledge.ArticleExpired` v1 | `knowledge.Expire` timer fires | outbox, same transaction as the timer completion |
| consumes | none | | |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `knowledge.ReviewDue` | publish; due = `expires_at − 1 month` | Logs only. **Follow-up:** notify the matching reviewer role (`notify.DeliveryRequested`) once a staff_role form for lawyer exists; do not route rights to `moderator` or reuse psychologist for rights. | cancelled when the version is retired; subject/dedup = version id |
| `knowledge.Expire` | publish; due = `expires_at` | Emits `knowledge.ArticleExpired`; row stays `published` (member availability comes from `expires_at`) | cancelled when retired; subject/dedup = version id |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `Service.PublishedFragments(ctx, lang, kind) ([]Fragment, error)` | Only current (non-expired) published versions of that kind/lang for the future retriever (ADR-0010). |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Settings.RightsTTL` | ≈ 6 months | Review period for rights after publish |
| `service.Settings.SelfhelpTTL` | ≈ 12 months | Review period for self-help after publish |

## Invariants

- Author cannot publish or reject own version (service; tests).
- Psychologist ↔ self-help, lawyer ↔ rights for authoring and review; admin creates topics of either kind (service; tests).
- At most one published version per article+language (partial unique index + retire on publish; tests).
- Publishing one language marks other non-retired languages `needs_sync` (service; tests).
- Rights publish requires `sourceUrl` (service).
- Expired rights are visible as `being_rechecked` but excluded from `PublishedFragments`; self-help stays `current` until replaced (service; tests).
- Side effects (outbox, timers, audit) commit with the domain write (ADR-0007 / ADR-0020).

## Failure modes

- Database down: every operation fails with 500; nothing half-written.
- Broker down: events wait in the outbox.
- Reviewer notification on `ReviewDue` is not sent yet (documented follow-up above).

## Tests

- Unit: `service/service_test.go` with `knowledgetest` fakes — author cannot publish, wrong role, one published + needs_sync, timers, expiry event, member visibility, source URL, reject, idempotent draft.
- Integration: `adapter/integration_test.go` with `testinfra.Postgres` — publish workflow and outbox/audit/timers, expire worker, member visibility, DEMO seed once.

## DEMO seed

`presets/demo/knowledge/articles.json` (titles/bodies contain `DEMO`) is loaded by `presets.LoadKnowledge` and inserted by `adapter.SeedDemoIfEmpty` from `birge migrate` only when knowledge tables are empty. If the knowledge migration is not yet applied, the seed is a no-op.
