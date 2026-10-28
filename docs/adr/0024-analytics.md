# ADR-0024: Pilot metrics and analytics

Status: Accepted · Date: 2026-10-27

## Context
The pilot must report the metrics of requirements § 9 (stress, isolation, activity and retention, tea funnel, SOS, escalations, rights) as aggregates with a CSV export (§ 10). The data behind them is mental-health data of a small group: ten mothers in a pilot can be re-identified from a small count. Account deletion must remove a member's analytics data too (N6, ADR-0009).

## Decision
- **Context `analytics`** owns the metrics. It never reads another context's tables.
- **Activity, funnels and timings come from domain events.** Analytics consumes the existing events (JetStream durable consumer, deduplicated through `platform.processed_events`) and stores one fact per event: event type, time, a small code (SOS type, escalation outcome, rating) and a pseudonymous actor. No payload, text or district is stored.
- **Pseudonymous actor:** `HMAC-SHA256(analytics key, member_id)`. The key is a secret from a file, separate from the crypto keys. Analytics can compute the pseudonym of a member it is told to delete, but cannot turn a pseudonym back into a member. On `privacy.DataDeletionRequested` it deletes that member's facts.
- **Health scores never leave wellbeing per person.** Wellbeing exposes cohort aggregates through its service package (PSS-4 at the baseline check-in and after 8 weeks, isolation before and after: count, mean, median). Analytics only formats and suppresses them.
- **Small-cell suppression:** every count or aggregate built from fewer than `k` members is reported as `<k`, never as the number. Default `k = 5`, set by configuration; the DEMO preset uses `k = 1` so demo data shows numbers, with the usual `DemoPreset` warning.
- **Retention:** facts are kept 24 months and then deleted by a timer; the weekly aggregates computed from them are kept without actors.
- **Access:** staff operations `GET /staff/metrics` (JSON) and `GET /staff/metrics.csv`, role `admin`, MFA, period up to 12 months, every export written to the audit log. Members have no access.
- Operational metrics (latency, errors, queue depth) are not analytics; they stay in Prometheus (ADR-0021).

## Consequences
A new event is counted only when analytics adds it to its consumer. Metrics start from the first event after deployment; there is no backfill from other contexts' tables. A cohort smaller than `k` shows no stress or isolation numbers, by design.

## Addendum 2026-10-28: inputs
The metrics of § 9 map to events as follows. Activity: `membership.MemberActiveDay` (new; once per member per day from the session middleware). Tea: `circle.TeaRequested` (new) → `circle.MatchAccepted` → `circle.AcquaintanceEstablished`. SOS: `emergency.SosRaised`, `SosResponseAccepted` (first response), `SosClosed`. Escalations: `safety.EscalationOpened`, `EscalationTaken`. Rights: `assistant.QuestionAnswered` (new; `scope=rights`, `source=knowledge`) and `assistant.ReplyRated`. Stress and isolation come from the wellbeing cohort aggregates, not from events. The response shape is `Metrics` in the contract; a cell below `k` carries only `suppressed: true`.

## Addendum 2026-10-28: implementation
What the code does where the decision left room (`internal/domains/analytics/README.md` has the details):
- **Subject pseudonym.** Besides the actor, a fact carries `subject = HMAC-SHA256(key, "subject|" + object id)` for the SOS, escalation, match, Tea request or assistant message it is about, so a closing, a first response or a taking joins its opening without storing an ID. A pair event (`MatchAccepted`, `AcquaintanceEstablished`) stores one fact per member.
- **Late events of a deleted member.** Events are consumed asynchronously, so one of hers can arrive after `privacy.DataDeletionRequested`. Deletion records her pseudonym in `analytics.erased_actors` and facts for it are never stored; a pseudonym is purged once it is older than the EVENTS stream's max age (`BIRGE_EVENTS_MAX_AGE`, the value the worker gives the stream), because an event of hers can reach analytics only while the stream holds it. Deletion also removes the actor-less closed/taken/rated facts of her SOS, escalations and assistant answers, and records their subject pseudonyms in `analytics.erased_subjects` (same window), so such a fact arriving later is dropped too. Storing a fact and erasing a member are serialized by a transaction advisory lock on the pseudonym (the actor, or the subject of an actor-less fact; deletion locks her actor and her subjects), taken before the erased check and before the deletes: under READ COMMITTED the check and the delete could otherwise interleave and leave a fact of hers.
- **Rights.** `assistant.QuestionAnswered` carries `member_id` (v1 amended before release), so its fact has the asker as actor (subject = message, code `<scope>_<source>`). `ReplyRated` names no member; its fact keeps only the message subject and is joined to the answer by that subject **at report time**, so the order of arrival does not matter: a rating delivered before its answer is stored without an actor and joins once the answer is stored. `answeredFromKnowledge` counts rights answers with source `knowledge` given in the period; `helpedShare` is, of the rights answers given in the period that have a rating (at any time), the share whose latest rating is `helped` (the member may change it; the last one wins). Members are the distinct askers behind each cell. A rating whose answer is not stored (never arrived, or erased with its member) joins nothing and is not counted.
- **Isolation is always suppressed.** The questionnaire (PHQ-4 + PSS-4) has no isolation item and answers are sealed, so wellbeing returns an empty aggregate until an isolation item with a plain score exists.
- **Stress cohort.** Members whose first check-in falls in the period; week 8 is the check-in nearest to baseline + 8 weeks within 6–10 weeks, and may lie after the period.
- **Retention cohort.** Members whose first active day falls in the period; retained at week 4 (8) when active on days 28–34 (56–62); only members whose window has ended count.
- **k on the DEMO preset.** `k = 1` applies when `BIRGE_ANALYTICS_K` is unset and the deployment runs on the DEMO settings preset (consent versions from presets); otherwise the default is 5.
- **Weekly roll-ups.** The retention sweep cuts at the Monday that starts the week 24 months ago (deployment zone), so each week is rolled up whole and once. Reports read facts only; roll-ups are kept for later reporting.
