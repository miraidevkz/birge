# circle

**In short:** Helps a mother find another mother nearby for tea or a call, and records when two of them have met in person, which later decides who may be a trusted contact.

Tea matching («Чай») and pairwise "met in person" («знакомы лично») acquaintances for Moms nearby. A verified member opens a tea request with format and preferences; an hourly job pairs open requests by score; both accept → a private chat will open (chat listens to `circle.MatchAccepted`); after three days both confirm they met → an acquaintance used by emergency trusted contacts and by the childcare rule (AGENTS.md non-negotiable 5). Serves `docs/requirements/mvp.md` § 3 and § 4.4.

## Owns

| Table (schema `circle`) | Purpose | Personal data |
|---|---|---|
| `tea_requests` | One tea request: format, preferences, status `open → matched\|cancelled\|expired`; `(member_id)` unique while open; `(member_id, idempotency_key)` unique | IDs only (names/districts/ages/needs read from membership at request time) |
| `matches` | Proposed pair (`member_a < member_b`), request ids, answers, met answers, status, optional `room_id` and `ask_met_at` | IDs only |
| `acquaintances` | Confirmed met-in-person pairs (`member_a < member_b`), `since`; primary key unique | IDs only |

## API

Operations of tag `circle` in `contracts/openapi/birge.yaml`. All are member-only: the caller is `auth.Principal.MemberID`; staff, anonymous and sessions without a member id → 401.

| Operation | Rules not in the contract |
|---|---|
| `POST /me/tea-requests` | Verified only (403). `Idempotency-Key` per member. At most one open request (409). Schedules `circle.RequestExpire` at `expires_at` and ensures `circle.MatchRun` is pending. Publishes `circle.TeaRequested` for a new request only. |
| `GET /me/tea-requests` | Newest 20. |
| `POST /me/tea-requests/{id}/cancel` | Owner only; open → cancelled; cancels expiry timer; idempotent. Another member's id → 404. |
| `GET /me/matches` | Pair cards for the caller: other first name, district id, child ages, shared needs — never contacts. |
| `POST /me/matches/{id}/answer` | Participant only. One answer per side; two accepts → `accepted`, `circle.MatchAccepted`, `circle.AskMet` after `AskMetAfter`; any decline once both answered → `declined`. |
| `POST /me/matches/{id}/met` | Participant only; match must be `accepted`. Two yes → `met`, acquaintance row, `circle.AcquaintanceEstablished`; any no once both answered → `not_met`. |
| `GET /me/acquaintances` | Confirmed pairs; other member's first name from membership. |

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `circle.TeaRequested` v1 | new tea request created | outbox, same transaction; `request_id`, `member_id`, `format` only (no preferences, no district); exactly once per request: an Idempotency-Key replay or a 409 second open request publishes nothing |
| publishes | `circle.MatchAccepted` v1 | both accept | outbox, same transaction; `match_id`, `member_a`, `member_b` only |
| publishes | `circle.AcquaintanceEstablished` v1 | both confirm met | outbox, same transaction; member ids only |
| publishes | `notify.DeliveryRequested` v1 | match proposed (`circle.match_proposed`); AskMet fires (`circle.ask_met`); class `normal`, recipient `member_id`, params `{match_id}` | outbox, same transaction; `request_id` = SHA-1 UUID of subject + template + member |
| consumes | `privacy.DataDeletionRequested` v1 | account deletion | JetStream durable `circle-deletion` (`wiring.DeletionConsumer`); deduplicated in `platform.processed_events` (consumer `circle.Deletion`) after `service.Deletion.DeleteMember`, which is idempotent and runs in one transaction |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `circle.MatchRun` | first open request (and after every run); subject = fixed `MatchRunSubject`; due = now + `MatchInterval` (1h) | pair open requests; notify both; cancel+reschedule self | dedup key = subject; cancel before schedule on the worker transaction |
| `circle.RequestExpire` | request created; due = `expires_at` (`RequestTTL`, 7d); subject = request id | expire if still open | cancelled on match or cancel |
| `circle.AskMet` | mutual accept; due = now + `AskMetAfter` (3d); subject = match id | notify both (`circle.ask_met`); set `ask_met_at` once | skipped when match is no longer `accepted` |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `Service.Met(ctx, a, b) (bool, error)` | Symmetric "met in person" for the pair (`true` only after both confirmed). For emergency's `Acquaintances` port. |
| `Service.ListAcquaintances(ctx, principal)` | Caller's acquaintances with first names (HTTP). |
| `Service.SetRoomID(ctx, matchID, roomID)` | Stores the private room id once. **Not called yet:** chat opens the direct room on `circle.MatchAccepted` but does not report its id back, so `room_id` stays null and `Match.roomId` is never present ([roadmap](../../../docs/roadmap.md)). The member finds the room in her chat list. |
| `Service.HandleMatchRun` / `HandleRequestExpire` / `HandleAskMet` | Timer handlers; used by `adapter.TimerHandlers`. |

Ports used by this context (wired in `internal/wiring`): `MemberInfo` (membership: verification, district, language, children birth years/needs, first name), `Blocks` (membership `IsBlocked`, both directions), `Store` (PostgreSQL adapter). `Service.Met` backs emergency's `Acquaintances` port for member trusted contacts.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Settings.RequestTTL` | 7d | Open request lifetime |
| `service.Settings.MatchInterval` | 1h | `circle.MatchRun` period |
| `service.Settings.AskMetAfter` | 3d | Delay before "did you meet?" |
| `service.Settings.RematchCooldown` | 30d | Same pair cannot be matched again within this window |
| `service.Settings.ScoreWeights` | district 3, age 2, need 2, language 1 | Matching bonuses (§ 4.4) |

## Invariants

- Verified members only create tea requests (service; 403).
- At most one open request per member (partial unique index + service 409).
- Matching never pairs blocked members either way (ADR-0019); block lookups fail closed.
- Same canonical pair is not rematched within `RematchCooldown` (service + `matches` index).
- Preferences are hard filters; score bonuses apply when attributes match (§ 4.4).
- Pairing is greedy by descending score; ties by earlier request `created_at` then request id (model; unit tests).
- Members stored canonically (`member_a < member_b`) on matches and acquaintances (table checks).
- No names, districts, ages or needs in logs or events — IDs and event names only (service; tests).
- Nothing is deleted in normal operation: requests cancel/expire, matches change status. The only delete queries belong to account deletion.
- Account deletion (AGENTS.md non-negotiable 6): her acquaintances (both directions) and every tea request no match references are deleted. Her matches stay as tombstones, with the two requests they reference (foreign keys): the other member's history keeps the pair, and her name, read from membership at request time, is no longer found (shown as deleted). A `proposed` match ends as `expired` (she can no longer answer), a still-open matched request is `cancelled`; the `RequestExpire` and `AskMet` timers of her requests and matches are cancelled, all in one transaction. One audit entry `circle.member_deleted` (actor system).

## Failure modes

- Database down: every operation fails with 500; nothing is half-written; timer worker retries with backoff (ADR-0020).
- Broker down: events wait in the outbox; relay publishes later (ADR-0007). Match notifications are delayed until notify receives `DeliveryRequested`.
- Membership down (`MemberInfo`/`Blocks`): create, list cards, matching and acquaintances fail (fail closed).
- `room_id` stays null: chat opens the direct room but nothing calls `SetRoomID` yet (see Service API).

## Tests

- Unit: `model/score_test.go` — scoring, preferences, greedy pairing, tie-break, blocks/rematch filters; `service/service_test.go` — create rules, `TeaRequested` once per request (replay, 409, rollback on outbox failure), matching and accept/met state machine, decline, blocked pairs, cancel/expire, SetRoomID, fail-closed blocks; logs and events checked for fake personal data.
- HTTP: `api/handler_test.go` — 401/403/409/201/204.
- Integration: `adapter/integration_test.go` with `testinfra.Postgres` — one open request, MatchRun timer re-schedule, expire timer, blocked pairs never matched, symmetric `Met`, no personal data in outbox payloads, `TeaRequested` outbox rows validated against the contract (`testinfra.ValidateEventPayload`) with replay and 409 adding none.
- Fakes: `circletest/`.
