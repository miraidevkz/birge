# wellbeing

**In short:** The weekly two-minute check-in on how she is, reminders, an 8-week history and short audio practices. It never gives a diagnosis.

Weekly well-being check-ins (How am I («Как я»): PHQ-4 + PSS-4), reminder schedules, an 8-week score history and audio practices. Serves `docs/requirements/mvp.md` § 4.2. Scoring thresholds and question texts are configuration; results are never a diagnosis.

## Owns

| Table (schema `wellbeing`) | Purpose | Personal data |
|---|---|---|
| `schedules` | Per-member weekday/hour reminder preference | none |
| `checkins` | Completed questionnaires with scores and flag | `answers` sealed (ADR-0009); scores/flag plain |
| `practices` | Audio practice metadata (object keys) | none |

## API

Operations of tag `wellbeing` in `contracts/openapi/birge.yaml`:

| Operation | Rules not in the contract |
|---|---|
| `GET/PUT /me/checkins/schedule` | Member session only (staff refused). PUT (re)schedules `wellbeing.Reminder` or cancels when disabled. |
| `GET /me/checkins/questionnaire` | Member only. Returns configured version, question ids, instruments, locale keys and options (no thresholds). |
| `POST /me/checkins` | Member only. `Idempotency-Key` unique per member. Validates answers against the current version; seals answers; emits `wellbeing.CheckinCompleted` (flag only) in the same transaction. `flag=crisis` → `CrisisOpener` + crisis phones; opener failure still returns phones and logs `CrisisEscalationFailed`. |
| `GET /me/checkins/history` | Member only. Points for the last N weeks (default 8), oldest first; scores only. |
| `GET /practices` | Member only. URLs = configured base URL + `object_key`. |

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `wellbeing.CheckinCompleted` v1 | check-in stored (first idempotent write) | outbox, same transaction; payload is ids + flag only |
| publishes | `notify.DeliveryRequested` v1 | reminder timer fires | outbox, same transaction as next-week schedule; template `wellbeing.checkin_reminder`, class `reminder` |
| consumes | `privacy.DataDeletionRequested` v1 | account deletion | JetStream durable `wellbeing-deletion` (`wiring.DeletionConsumer`); deduplicated in `platform.processed_events` (consumer `wellbeing.Deletion`) after `service.Deletion.DeleteMember`, which is idempotent and runs in one transaction |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `wellbeing.Reminder` | enabled schedule set/updated (dedup key = member id) | publish DeliveryRequested; schedule next weekly occurrence in the deployment time zone | cancel+reschedule on the worker transaction; disabled schedule cancels |

## Service API for other contexts

| Method | Caller | Rules |
|---|---|---|
| `Service.CohortAggregates(ctx, from, to)` | analytics, through its port implemented in `internal/wiring` (ADR-0024) | Cohort = members whose **first** check-in is in `[from, to)`. Stress baseline = PSS-4 at that check-in; week 8 = her check-in nearest to baseline + 8 weeks within 6–10 weeks (earlier on a tie; may lie after `to`). Returns count, mean and median only, never a per-member value. Isolation stays empty (count 0): the questionnaire has no isolation item and answers are sealed. No HTTP and no member check; analytics enforces staff role, MFA, audit and small-cell suppression. |

safety implements `CrisisOpener`; notify consumes DeliveryRequested.

Ports used by this context (wired in `internal/wiring`): `Sealer` (platform envelope, AD `wellbeing.checkins.answers\|<id>`), `CrisisOpener` → `safety.OpenEscalation`.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Settings.Questionnaire` | required | Current version and questions (ids, instrument, text keys, options) from `BIRGE_QUESTIONNAIRE_FILE` or the EXAMPLE preset when unset |
| `service.Settings.ReverseScoreQuestionIDs` | empty | Question ids scored as maxOption−value (PSS reverse items) |
| `service.Settings.CrisisItemQuestionID` | empty | Max answer on this item forces `flag=crisis` |
| `service.Settings.Thresholds` | required | Per-instrument `ElevatedAt` / `CrisisAt` |
| `service.Settings.FeedbackByFlag` | required | Non-diagnostic feedback key + suggestion per flag |
| `service.Settings.TimeZone` | `Asia/Almaty` | Deployment IANA zone for reminders (`BIRGE_TIME_ZONE`; UTC+5 since March 2024; **hosts must ship current tzdata**) |
| `service.Settings.PracticesBaseURL` | required | In-jurisdiction object-storage base for practice URLs (`BIRGE_PRACTICES_BASE_URL`) |
| `service.Settings.CrisisPhones` | empty | Returned on every crisis result (`BIRGE_CRISIS_PHONES`) |
| see `docs/development.md` § Configuration | | all variables, defaults and which subcommand reads them |

## Invariants

- Answers sealed with associated data `wellbeing.checkins.answers|<checkin id>` (service + ADR-0009).
- Events and logs: IDs, event names and flag only — never answers, scores or question text (service; tested).
- Crisis phones returned even when `CrisisOpener` fails (service; tested).
- Questionnaire answers: every configured question answered exactly once with a legal option (model).
- PHQ-4 0–12, PSS-4 0–16 after reverse scoring (model).
- Reminder next occurrence computed with `time.LoadLocation` in the deployment zone; no in-process cron (ADR-0020).
- Cohort aggregates expose count, mean and median only; per-member scores never leave the service package (model `BuildCohortAggregates`; ADR-0024).
- Account deletion (AGENTS.md non-negotiable 6): her schedule and every check-in are deleted and her pending `wellbeing.Reminder` timer is cancelled, in one transaction. Nothing is tombstoned: no other record refers to a check-in (the safety escalation it may have opened keeps only IDs). One audit entry `wellbeing.member_deleted` (actor system).

## Failure modes

- Database down: HTTP and reminder handler fail; platform timer retries with backoff (ADR-0020).
- Outbox/broker down: check-in/reminder rows and timers commit with the outbox; relay catches up (ADR-0007).
- `CrisisOpener` down: check-in still stored and emitted; client receives `flag=crisis` and phones; log `CrisisEscalationFailed`.
- Sealer down: submit fails before insert; no partial check-in.
- Missing/stale tzdata: `SetSchedule` / reminder handler error until the host zone database is updated.

## Tests

- Unit: `model/cohort_test.go`, `service/cohort_test.go` — cohort selection by first check-in, follow-up window and ties, mean/median, bad period, store failure.
- Unit: `model/model_test.go`, `service/service_test.go` — scoring (reverse + thresholds + crisis item), validation, crisis phones on opener failure, next-occurrence across the week in Asia/Almaty, schedule enable/disable.
- Integration: `adapter/integration_test.go` — PostgreSQL store, outbox payload (flag only), reminder worker, cohort aggregates with `testinfra.Postgres`.
- Fakes: `wellbeingtest/` (memory store, sealer, crisis opener).
