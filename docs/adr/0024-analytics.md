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
