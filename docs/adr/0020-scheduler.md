# ADR-0020: Durable timers on PostgreSQL

Status: Accepted · Date: 2026-10-27

## Context
The product depends on timers: escalation backup after 15 minutes, "did you meet?" after 3 days, tea expiry after 7 days, listing expiry after 30 days, AI message deletion after 90 days, weekly check-in reminders in the member's time zone. In-memory timers duplicate across replicas and are lost on restart.

## Decision
- A `timers` table in `platform`: `id`, `kind`, `subject_id`, `due_at`, `payload`, `attempts`, `locked_until`, `done_at`.
- A timer is created in the same transaction as the domain change that needs it (for example, an escalation and its 15-minute timer).
- Worker replicas claim due timers with `SELECT … FOR UPDATE SKIP LOCKED`, emit the corresponding event through the outbox and mark the timer done in the same transaction. Handlers are idempotent; failed attempts back off.
- Recurring schedules (weekly reminders) store the next occurrence, computed in the member's time zone; firing schedules the next one.
- Bulk jobs (retention, re-indexing) are timers too, so there is one mechanism and one place to monitor.

## Alternatives
River: mature, but open-core (paid Pro tier); a small in-house component avoids the dependency (ADR-0012). In-memory or Kubernetes CronJobs: lose timers or duplicate them.

## Consequences
Metric and alert: oldest overdue timer age. Timer kinds are documented in the owning context's README.
