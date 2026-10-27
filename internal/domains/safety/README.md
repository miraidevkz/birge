# safety

The crisis path (ADR-0023): level-1 crisis detection on a local lexicon, escalations to a person (`open → taken → closed`) with a 15-minute backup alert, and the duty roster that tells notify whom to text. Serves `docs/requirements/mvp.md` § 5 (pipeline step 1 and step 6, crisis response and escalation) and § 6 (moderation queue: “Taking it” («Беру»), “Close with comment” («Закрыть с комментарием»), duty roster). It never depends on the LLM or the network to it.

## Owns

| Table (schema `safety`) | Purpose | Personal data |
|---|---|---|
| `escalations` | One row per request for a person; lifecycle enforced by `escalations_lifecycle_check`; at most one open-or-taken row per member (`escalations_active_member_idx`) | `member_id` (pseudonymous ID); `note` encrypted with the member's data key (AD `safety.escalations.note\|<id>`) |
| `duty_shifts` | Primary and backup shifts per role; id is the client's Idempotency-Key; cancelled, never deleted | `phone` encrypted with the staff member's data key (AD `safety.duty_shifts.phone\|<id>`) |

Writes to `platform.audit_log` through the `Auditor` port (see Open questions).

## API

Operations of tag `safety` in `contracts/openapi/birge.yaml`. No principal → 401; a principal without the required kind or role → 403 (checked in the service, `service/staff.go`).

| Operation | Rules not in the contract |
|---|---|
| `POST /me/help-requests` | Member session only (staff → 401). Idempotent per member while an escalation is open or taken: returns the same id and alerts nobody again, which also bounds SMS from repeated presses. The `Idempotency-Key` is accepted but not needed for that. Returns `Settings.CrisisPhones`. |
| `GET /staff/escalations` | Staff with role `psychologist` or `moderator`. Newest first, 50 per page, opaque keyset cursor. Items carry source, status, times and district only; `takenByMe` when taken. |
| `POST /staff/escalations/{id}/take` | Queue roles. `open` → `taken`, cancels `safety.EscalationBackup`, publishes `safety.EscalationTaken`, audit `escalation.take`, one transaction. Taking again by the same person returns it unchanged; by anyone else, or when closed → 409. |
| `POST /staff/escalations/{id}/contact` | Queue roles, and only the staff member who took it while it is `taken` (otherwise 403). Audit `escalation.reveal_contact` is committed before the name and phone are read from `MemberDirectory`. `Cache-Control: no-store`. |
| `POST /staff/escalations/{id}/close` | Only the staff member who took it (403 otherwise; 409 when open or closed). Outcome required; note optional (≤ 4000 bytes), sealed. Publishes `safety.EscalationClosed`, audit `escalation.close`. |
| `GET /staff/duty-roster` | Staff with `psychologist`, `moderator` or `admin`. Window ≤ 62 days, at most 500 shifts. Phones are never returned. |
| `POST /staff/duty-roster` | `admin` only. The Idempotency-Key becomes the shift id: a repeat returns the stored shift. Phone must be E.164; shift ≤ 7 days and must not have ended. Audit `duty_shift.add`. |
| `DELETE /staff/duty-roster/{id}` | `admin` only. Sets `cancelled_at` (no row is deleted). A started shift → 409; cancelling twice → 204. Audit `duty_shift.cancel`. |

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `safety.EscalationOpened` v1 | new escalation | outbox, same transaction as the insert, alert and timer |
| publishes | `notify.DeliveryRequested` v1 | new escalation (`staff_role=psychologist`, template `safety.escalation_opened`) and backup timer (`psychologist_backup`, `safety.escalation_unattended`); class `critical`, params `{escalation_id}` | outbox, same transaction; `request_id` derived from escalation id + template, so retries deduplicate in notify |
| publishes | `safety.EscalationTaken` v1 | take | outbox, same transaction as the update, timer cancel and audit |
| publishes | `safety.EscalationClosed` v1 | close | outbox, same transaction |
| publishes | `safety.EscalationUnattended` v1 | backup timer fires on a still-open escalation | outbox, in the timer worker's transaction |
| consumes | none | | |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `safety.EscalationBackup` | escalation opened; due after `Settings.BackupAfter` (15 min); subject and dedup key = escalation id | `adapter.TimerHandlers.EscalationBackup` → `HandleEscalationBackup` in the worker's transaction: if still `open`, publishes `EscalationUnattended` and the backup alert; if taken, closed or unknown, nothing | cancelled on take (and again on close); row locked `FOR UPDATE`; events commit with the timer's completion |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `Service.Detect(text, lang) CrisisResult` | Level-1 lexicon, pure and local; checks every loaded language, `lang` first. Never logs the text. Returns `{Crisis, Category, Lang}`. |
| `Service.OpenEscalation(ctx, memberID, source) (id, alreadyOpen, err)` | Idempotent per member while open or taken; safe under concurrent calls (partial unique index). Insert, `EscalationOpened`, critical alert and backup timer commit together. The district lookup is best-effort (2 s) and never blocks opening. No authorization: callers pass their own member's id. |
| `Service.ResolveStaffRole(ctx, role) ([]ExternalRecipient, error)` | For notify's recipient resolution of `staff_role`: shifts covering now (`psychologist` primary, `psychologist_backup` backup, `moderator`), each `{Ref: shift id, PhoneE164, Lang}`. `ErrNoDutyCoverage` (and an Error log `NoDutyCoverage`, role only) when none; a shift whose phone does not open is skipped and logged by shift id. |
| `Service.ShiftPhone(ctx, shiftID) (string, error)` | For notify's `PhoneFor(kind=staff)`: unseals the on-duty phone; `ErrNotFound` for a cancelled or unknown shift. Never logged. |
| `Service.CrisisPhones()` | The configured crisis lines. |

Ports used by this context (wired in `internal/wiring`): `Sealer` (platform envelope), `MemberDirectory` (membership + Kratos phone), `Store`/`Auditor` (PostgreSQL adapter).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Settings.BackupAfter` | 15m | Delay before the backup shift is alerted (`BIRGE_ESCALATION_BACKUP_AFTER`) |
| `service.Settings.CrisisPhones` | empty | Crisis lines returned to the member (`BIRGE_CRISIS_PHONES`) |
| `service.Settings.StaffLang` | `ru` | Language of SMS alerts to on-duty phones (`BIRGE_STAFF_SMS_LANG`) |
| `Service.Lexicon` | required | `crisis.LoadDir(BIRGE_CRISIS_LEXICON_DIR)`; startup refuses an empty or unloadable lexicon |
| see `docs/development.md` § Configuration | | all variables, defaults and which subcommand reads them |

### Lexicon format

One UTF-8 file per language and category, `<kk|ru>.<category>.txt`; one entry per line: a phrase (`хочу умереть`), a prefix phrase (`покончит* с собой`), or `re: <RE2>` over the normalised text; `#` starts a comment line. Full rules in `crisis.Load` and the package documentation (normalisation, fuzzy and compact matching). `crisis/testdata/demo/` is a DEMO lexicon for tests only.

## Invariants

- At most one open-or-taken escalation per member (partial unique index; `OpenEscalation`; integration test with 20 concurrent calls).
- Lifecycle `open → taken → closed`; a closed row has a taker, an outcome and `closed_at` (check constraint; service).
- Escalation insert, its events and its timer are atomic (service + store; integration test with injected failures).
- Taking cancels the backup timer; the timer handler never alerts for a taken or closed escalation (service; unit and integration tests).
- Contact is revealed only to the taker while taken, and every reveal is audited before the contact is read (service; tests).
- The queue never shows names or phones; phones are never returned by the roster API (api; tests).
- Notes and on-duty phones are stored sealed only (service; schema comments).
- Shifts and escalations are never deleted (no delete query exists).
- Logs: IDs, roles and event names only; never message text, names, phones or notes (service; `newEnv` test cleanup checks).
- `crisis.Detect` does no I/O and allocates no shared state; DEMO-set recall must stay 100% (`TestDemoLexiconEvalSet`).

## Failure modes

- Database down: `OpenEscalation` fails and the caller must still show the fixed crisis screen and phones (the client never waits for the server, ADR-0023); help requests return 500; the timer worker retries with backoff.
- Broker down: events wait in the outbox; the relay publishes later (ADR-0007). Staff SMS are delayed until notify receives `DeliveryRequested`.
- No duty coverage: `ResolveStaffRole` returns `ErrNoDutyCoverage`; notify retries the message; the admin queue is the second channel. ADR-0023 asks for a monitoring alert when the next 24 h are not covered (not implemented here).
- Membership (directory) down: escalations open without a district (`DistrictLookupFailed` warning); contact reveal fails after the audit entry is written.
- Sealer down: closing with a note and adding shifts fail (nothing is committed); resolving roles skips shifts whose phone cannot be opened.
- Lexicon missing: `Detect` returns no crisis and logs `LexiconMissing` on every call; wiring must refuse to start instead (`crisis.ErrEmptyLexicon`).

## Tests

- Crisis: `crisis/crisis_test.go` — normalisation, loader errors, fuzzy and compact matching, the evaluation set `evals/datasets/crisis/*.tsv` against the DEMO lexicon (100% recall required, false-positive rate logged), benchmark.
- Unit: `service/service_test.go` with fakes from `safetytest/` — idempotency, rollback on failure, roles, take/reveal/close rules, sealing, timer behaviour, roster, role resolution, pagination, no personal data in logs.
- HTTP: `api/handler_test.go` — status codes, `Cache-Control: no-store`, no member data in queue or roster responses.
- Integration: `adapter/integration_test.go` with `testinfra.Postgres` — 20 concurrent opens give one escalation, one event pair and one timer; atomicity under injected failures; take cancels the timer and the worker fires only the untaken one; audit rows; constraints.

## Open questions

- **Audit insert is temporary.** `adapter/audit.go` writes `platform.audit_log` with a raw SQL statement because platform has no audit API yet; replace it with a platform audit package.
- `Escalation.districtId` is required in the contract but unknown when membership has no district or is unavailable; the API then returns the nil UUID. The contract should make it optional.
- Staff SMS language is one setting (`StaffLang`); shifts have no language column.
- notify re-resolves external recipients at send time as `trusted_contact_id` (`model.ResolvedRecipient.AsRecipient`); staff-shift recipients need their own form there, or the SMS timer cannot re-resolve them.
- Nobody can reassign an escalation taken by someone who then becomes unavailable; reassignment needs a rule and an audited operation.
