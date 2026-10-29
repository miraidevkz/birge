# analytics

**In short:** Counts how the pilot is going (stress, activity, Tea, SOS, escalations) without ever naming a person, and gives the programme team a CSV.

The pilot metrics of `docs/requirements/mvp.md` § 9 (stress, isolation, activity and retention, Tea funnel, SOS, escalations, rights) and their CSV export (§ 6), as aggregates only (ADR-0024). Analytics stores one fact per consumed domain event, names people only by pseudonym (`HMAC-SHA256(analytics key, member_id)`), and suppresses every cell built from fewer than `k` members. Stress and isolation come from the wellbeing cohort aggregates through a port; analytics never reads another context's tables.

## Owns

| Table (schema `analytics`) | Purpose | Personal data |
|---|---|---|
| `facts` | One fact per consumed event (two for a pair event): `event_type`, `occurred_at`, `code` (short enum from the payload: SOS type, escalation source, Tea format, `<scope>_<source>`, `helped`/`not_helped`), `actor` (member pseudonym, 32 bytes, or NULL), `subject` (pseudonym of the SOS, escalation, match, request or message, so later events of one object join) | pseudonyms only; no IDs, payloads, text or districts |
| `erased_actors` | Pseudonyms of deleted members, so a late event of hers is not stored; purged once older than the EVENTS stream's max age (`BIRGE_EVENTS_MAX_AGE`) | pseudonyms only |
| `erased_subjects` | Subject pseudonyms of a deleted member's SOS, escalations and assistant answers, so a late actor-less fact about them (closed, taken, rated) is not stored; same window as `erased_actors` | pseudonyms only |
| `weekly_rollups` | Per week (Monday, deployment zone), event type and code: facts and distinct members, kept after the facts expire | none (counts) |

Uses `platform.processed_events` (consumer `analytics.Facts`), `platform.audit_log` and `platform.timers`.

## API

Operations of tag `analytics` in `contracts/openapi/birge.yaml` (`/v1/staff/*`, staff OIDC). Both need a staff principal with role `admin` and MFA (deny by default: members, anonymous → 401; other staff roles, no MFA → 403).

| Operation | Rules not in the contract |
|---|---|
| `GET /staff/metrics` | `from`/`to` are dates in the deployment zone, inclusive; `to` before `from` or more than 12 months after it → 400 `invalid_period`. The read writes `analytics.metrics_read` (object type `analytics.metrics`, actor role `admin`) in the transaction of its queries: a report that cannot be audited is not served. `Cache-Control: no-store`. |
| `GET /staff/metrics.csv` | Same period rule and cells. Audited as `analytics.metrics_export`. Columns `metric,bucket,members,value,suppressed` with a header row, UTF-8; `metric` is the contract field path (`sos.closedShare`), `bucket` the week or month date for `activity.weekly`/`activity.monthly`; a suppressed row has empty `members` and `value`. Every field starting with `= + - @`, tab or CR is prefixed with `'` (formula injection; none of ours can). `Content-Disposition: attachment; filename="birge-metrics-<from>-<to>.csv"`. |

### How each metric is computed

The period is `[from 00:00, to + 1 day 00:00)` in the deployment zone. `members` is the number of distinct members a cell is built from.

| Metric | Value | Members |
|---|---|---|
| `stress.baseline` / `stress.week8` | wellbeing cohort: PSS-4 at the first check-in of members whose first check-in is in the period, and at her check-in nearest to 8 weeks later (6–10 weeks) | wellbeing count |
| `isolation.before` / `after` | wellbeing cohort; always empty today (no isolation item in the questionnaire) | 0 → suppressed |
| `activity.weekly` / `monthly` | distinct members with a `MemberActiveDay` in the bucket (days inside the period only) | same |
| `activity.retention4w` / `8w` | of the members whose first active day is in the period and whose week-4 (days 28–34) or week-8 (days 56–62) window has ended, the share active in it | eligible members |
| `tea.requests` / `pairs` / `meetings` | `TeaRequested` / `MatchAccepted` / `AcquaintanceEstablished` in the period, counted by subject | distinct members |
| `sos.raised` | `SosRaised` in the period | distinct authors |
| `sos.closedShare` | of those, the share with a `SosClosed` (at any time) | distinct authors |
| `sos.firstResponseMedianMinutes` | median minutes from raising to the first `SosResponseAccepted` | authors with a response |
| `escalations.opened` | `EscalationOpened` in the period (every source) | distinct members |
| `escalations.takenWithin15mShare` | of those, the share with an `EscalationTaken` within 15 minutes | distinct members |
| `rights.answeredFromKnowledge` | `QuestionAnswered` with code `rights_knowledge` in the period | distinct askers (QuestionAnswered actor) |
| `rights.helpedShare` | of the rights answers (`rights_*`) given in the period that have a `ReplyRated` (at any time), the share whose latest rating is `helped` | distinct askers of the rated answers |

A `ReplyRated` fact has no actor. It is joined to its `QuestionAnswered` by the message subject when the report runs (`RightsOutcomes`), not when it is stored, so arrival order does not matter: a rating delivered before its answer simply waits in `facts` until the answer is stored. A rating whose answer is not stored joins nothing and is not counted.

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| consumes | `membership.MemberActiveDay` v1 | fact at noon of `day` in the deployment zone (an hour of tzdata drift keeps it on its day) | JetStream durable `analytics-facts`; deduplicated in `platform.processed_events` (consumer `analytics.Facts`) in the transaction of the facts |
| consumes | `circle.TeaRequested`, `circle.MatchAccepted`, `circle.AcquaintanceEstablished` v1 | facts (one per member of a pair) | as above |
| consumes | `emergency.SosRaised`, `emergency.SosResponseAccepted`, `emergency.SosClosed` v1 | facts; closed has no actor | as above |
| consumes | `safety.EscalationOpened`, `safety.EscalationTaken` v1 | facts; the staff member who took it is never stored | as above |
| consumes | `assistant.QuestionAnswered` v1 | fact; actor = `member_id`, subject = message, code `<scope>_<source>` | as above |
| consumes | `assistant.ReplyRated` v1 | fact without actor (subject = message, code `helped`/`not_helped`); joined to its answer at report time | as above |
| consumes | `privacy.DataDeletionRequested` v1 | account deletion | JetStream durable `analytics-deletion` (`wiring.DeletionConsumer`), processed consumer `analytics.Deletion` after `service.Deletion.DeleteMember` (idempotent, one transaction) |

A malformed payload is terminated, not redelivered (`AnalyticsEventMalformed`, logged by subject only). Facts older than 24 months on arrival are not stored. A new event counts only once it is added to `model.ConsumedEvents`; there is no backfill from other contexts.

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `analytics.Retention` | worker start (`EnsureRetentionTimer`) and by each run; daily 03:30 UTC, dedup key per day | rolls the facts before the cutoff (Monday 00:00 of the week 24 months ago, deployment zone) into `weekly_rollups`, deletes them and erased actor and subject pseudonyms older than the EVENTS stream's max age (none if `Settings.EventsMaxAge` is unset), schedules the next run | runs in the worker's transaction; a week is rolled once (cutoff on a week start, `ON CONFLICT DO NOTHING`, its facts deleted in the same transaction) |

## Service API for other contexts

None. Analytics consumes `wellbeing` cohort aggregates through `port.Cohorts`, implemented by `wiring.WellbeingCohorts` over `wellbeing/service.CohortAggregates`.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `BIRGE_ANALYTICS_KEY_FILE` | required (worker) | pseudonym key, at least 32 bytes, its own key (not the plan link key). Replacing it splits every member into a new pseudonym: retention and distinct counts across the change are wrong, and deletion no longer finds facts made with the old key |
| `BIRGE_ANALYTICS_K` | 5; 1 on the DEMO settings preset | small-cell threshold (API) |
| `BIRGE_TIME_ZONE` | `Asia/Almaty` | periods, weeks and months |
| `BIRGE_EVENTS_MAX_AGE` | `720h` | EVENTS stream max age (wiring); `Settings.EventsMaxAge` (worker) keeps erased pseudonyms this long. Unset (zero) keeps them forever |
| see `docs/development.md` § Configuration | | all variables |

## Invariants

- No member or object ID in clear, no payload, no text, no district in any analytics table (schema comment; integration test checks that no fact contains a seeded ID).
- Every cell from fewer than `k` members carries only `suppressed: true`; one function applies it (`model.Suppress`, unit tests at k−1, k and k=1).
- A cell built from no member is suppressed even with `k = 1`.
- Reports: admin + MFA, period ≤ 12 months, audit entry in the same transaction (service tests, integration test).
- Account deletion (AGENTS.md non-negotiable 6): her facts (her rights answers among them), the actor-less closed/taken/rated facts of her SOS, escalations and assistant answers, in one transaction with her pseudonym recorded in `erased_actors` and one audit entry `analytics.member_deleted`. Her SOS, escalation and answer subjects go to `erased_subjects`. Both tables keep the pseudonyms as long as the EVENTS stream keeps an event, so that her events still in the stream are dropped (deletion e2e test). A closing, taking or rating that arrives late for an object analytics never stored (its opening was late too and dropped) is stored without an actor, joins nothing and is never counted.
- Storing a fact and erasing a member never interleave: both take `pg_advisory_xact_lock` on the pseudonym (the fact's actor, else its subject; deletion takes her actor, then her subjects in order) before the erased check and before the deletes; the facts of one event are inserted in `model.LockOrder` (`TestErasureSerializedWithFactInsert`).
- Retention: facts 24 months; weekly roll-ups without actors are kept.

## Failure modes

- Database down: the consumer naks and JetStream redelivers (at most 10 times; metrics may then miss that event); reports fail with 500.
- Wellbeing aggregates fail: the report fails (no partial report is audited).
- Key missing on the worker: the worker does not start (configuration error).
- Broker down: facts arrive late; nothing is lost while the stream keeps the events.

## Tests

- Unit: `model/model_test.go` (period, buckets, suppression at k−1/k/k=1, CSV and formula guard, event → fact mapping, pseudonyms, retention cutoff), `service/service_test.go` (deny by default for every staff role, period limit, audit of read and export, suppression, failures, dedup, deletion), `api/handler_test.go` (status mapping, headers, CSV, suppressed cell shape).
- Integration: `adapter/integration_test.go` with `testinfra.Postgres` and `testinfra.NATS` — every metric from a seeded event sequence with a fixed clock, the rights metrics (`TestRightsMetrics`: rating before and after its answer, changed rating, suppression at k−1/k, deletion and late events), audit rows, deletion and late events, the retention timer (erased pseudonyms kept within and purged after the stream max age), the JetStream consumer. Payloads are checked against the event contracts (`testinfra.ValidateEventPayload`).
- Account deletion: `internal/wiring/deletion_e2e_test.go`.
