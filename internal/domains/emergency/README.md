# emergency

SOS and the emergency plan («SOS», «План ЧС»): a member raises an SOS that goes at once to the moderators and to her confirmed trusted contacts, may share it (verified members only, never «Не с кем оставить ребёнка») to her district feed where verified neighbours press «Могу помочь», and keeps a plan of a sealed child card and up to three trusted contacts she can send it to. Serves `docs/requirements/mvp.ru.md` § 4.6, § 3 (who may do what) and § 7 (`sos_requests`, `child_cards`, `trusted_contacts`). «Мне очень плохо» is not here: it is safety's `requestHumanHelp` with source `sos` (ADR-0023).

## Owns

| Table (schema `emergency`) | Purpose | Personal data |
|---|---|---|
| `sos_requests` | One row per SOS: type, status `open → closed` (`author`/`auto`), auto-close time, district when shared; `(member_id, idempotency_key)` unique | `content` (the text) sealed with the author's key (AD `emergency.sos.text\|<id>`); `member_id` pseudonymous |
| `sos_responses` | «Могу помочь», one per responder and SOS; `accepted_at` when the author chose her | IDs only |
| `child_cards` | One child card per member; `last_sent_at` | `content` sealed with the member's key (AD `emergency.child_cards.text\|<member id>`) |
| `trusted_contacts` | Up to 3 active contacts per owner: `member` (invited → confirmed) or `external` (confirmed at once); `plan_sent_at`; removed with `removed_at`, never deleted | external `name` and `phone` sealed with the owner's key (AD `emergency.trusted_contacts.name\|<id>`, `….phone\|<id>`); `lang` |
| `plan_sends` | One row per confirmed «Отправить план» (Idempotency-Key); also the plan-send rate limit | IDs and counts only |

Writes to `platform.audit_log` through the `Auditor` port (`internal/platform/audit`).

## API

Operations of tag `emergency` in `contracts/openapi/birge.yaml`. All are member-only: the caller is `auth.Principal.MemberID`; staff, anonymous and sessions without a member id → 401. Another member's SOS or contact → 404 (existence is not revealed).

| Operation | Rules not in the contract |
|---|---|
| `POST /me/sos` | Text 1..300 characters, not blank. Idempotent per member and `Idempotency-Key` (a replay returns the stored SOS, alerts nobody again and does not count). At most 3 per rolling hour, counted in the database under a per-member advisory lock → 429. One transaction: insert, `emergency.SosRaised`, critical alert to `staff_role=moderator`, a critical alert per confirmed contact, `emergency.SosAutoClose` timer. Invited contacts are never alerted. |
| `GET /me/sos` | Newest 20 with response counts; texts opened for the author. |
| `POST /me/sos/{id}/share` | Author only. Childcare → 409 always (N5); closed → 409; not verified → 403; no district → 409. Sharing again returns it unchanged. Publishes `emergency.SosShared`. |
| `POST /me/sos/{id}/close` | Author only; cancels the timer, publishes `emergency.SosClosed` once; idempotent. |
| `GET /sos/feed` | Verified only (403). Open, shared, unexpired SOS of the caller's district, never childcare, never her own, never of a member blocked either way; 50 per page, keyset cursor; a page can be shorter than 50 after block filtering while `nextCursor` is set. No district → empty page. Texts are opened under the author's key; an unopenable item (shredded author) is skipped. |
| `POST /sos/{id}/respond` | Verified only (403). Only an SOS visible in the caller's feed, else 404 (also for childcare, closed, own, blocked). Idempotent per responder; the first response notifies the author (`member_id`, class `normal`, template `emergency.sos_responded`, params `{sos_id}`). |
| `GET /me/sos/{id}/responses` | Author only. Oldest first, at most 100; blocked responders and members unknown to membership are left out; `metInPerson` from `Acquaintances.Met`. |
| `POST /me/sos/{id}/responses/{rid}/accept` | Author only; the response must belong to the SOS and its responder not be blocked (404). Publishes `emergency.SosResponseAccepted` once; repeats → 202; a new acceptance on a closed SOS → 409. |
| `GET /me/emergency-plan` | Card (`""` when none), active contacts (member contacts: display name from membership; external: opened name and `phoneMasked` such as `+7 *** *** 00 01`; never the full number), `lastSentAt`. |
| `PUT /me/emergency-plan/child-card` | ≤ 2000 characters; sealed. |
| `POST /me/trusted-contacts` | `Idempotency-Key` per owner. At most 3 active (invited + confirmed) under a per-owner lock → 409; a member already active → 409. `member`: needs `memberId` only, `Acquaintances.Met(owner, member)` (403 otherwise, also when blocked either way), status `invited`, invitation delivery to her (`member_id`, class `normal`, template `emergency.trusted_invitation`, params `{contact_id}`). `external`: needs `name` (1..60) and E.164 `phone` only; sealed; `confirmed` at once; SMS language = the owner's language when added. |
| `DELETE /me/trusted-contacts/{id}` | Owner only; sets `removed_at` (no deletion path); idempotent. |
| `POST /me/trusted-invitations/{id}/accept` | Only the invited member (anyone else, or a removed invitation → 404). Re-checks `Met` (403) and blocks (404). Idempotent. |
| `POST /me/emergency-plan/send` | `confirm: SEND`; `Idempotency-Key` per member. Needs a card and ≥ 1 confirmed contact (409). At most 3 sends per hour (429: external contacts get an SMS). One transaction: `plan_sends` row, `plan_sent_at` on confirmed contacts, `last_sent_at`, `emergency.PlanSent`, a delivery per confirmed contact (class `normal`, template `emergency.plan_sent`, params `{owner_id}`). |
| `GET /trusted/plans/{ownerId}` | Only an active, confirmed member contact of that owner whose `plan_sent_at` is set; anything else → 403 without saying why. Audit `emergency_plan.view_shared` (actor = member, object `emergency.child_card` / owner id) is written in the same transaction as the access check, before the card is opened; a failed audit returns nothing. `Cache-Control: no-store`. Shows the owner's current card. |

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `emergency.SosRaised` v1 | new SOS (not on replay) | outbox, same transaction as the insert, alerts and timer |
| publishes | `notify.DeliveryRequested` v1 | SOS raised (moderators: `staff_role=moderator`, `emergency.sos_raised`; contacts: `member_id` or `trusted_contact_id`, `emergency.sos_raised_contact`; class `critical`, params `{sos_id}`); first response; member invitation; plan sent | outbox, same transaction; `request_id` = SHA-1 UUID of subject + template + recipient, so retries deduplicate in notify; template keys and IDs only |
| publishes | `emergency.SosShared` v1 | first share | outbox, same transaction |
| publishes | `emergency.SosClosed` v1 | close by author or by the timer | outbox, same transaction |
| publishes | `emergency.SosResponseAccepted` v1 | first acceptance of a response | outbox, same transaction |
| publishes | `emergency.PlanSent` v1 | plan sent (not on replay) | outbox, same transaction |
| consumes | none | | |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `emergency.SosAutoClose` | SOS raised; due at `closes_at` (`Settings.AutoCloseAfter`, 24 h); subject and dedup key = SOS id | `adapter.TimerHandlers.SosAutoClose` → `HandleAutoClose` in the worker's transaction: an open SOS is closed (`auto`) and `SosClosed` published; closed or unknown → nothing | cancelled when the author closes; row locked `FOR UPDATE`; the feed also hides SOS past `closes_at` if the worker is late |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `Service.ContactPhone(ctx, trustedContactID) (phone, lang, err)` | For notify at SMS send time: the external contact's E.164 phone (opened with the owner's key) and SMS language (`kk`/`ru`). `service.ErrNotFound` when unknown, removed or a member contact. Never log or store the phone. |
| `Service.TrustedContactTarget(ctx, id) (memberID *uuid.UUID, external bool, err)` | What a `trusted_contact_id` stands for: a confirmed member contact (`memberID`) or an external one (`external`). `service.ErrNotFound` when unknown, removed or still invited. |
| `Service.HandleAutoClose(ctx, tx, sosID)` | Timer handler; used by `adapter.TimerHandlers`. |

Ports used by this context (wired later): `Sealer` (platform crypto), `MemberInfo` (membership: verification, district, display name, language), `Blocks` (membership `IsBlocked`, both directions), `Acquaintances` (circle `Met`), `Store`/`Auditor` (PostgreSQL adapter).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Settings.AutoCloseAfter` | 24h | Time after which an open SOS closes by itself (§ 4.6 «конфиг») |
| TODO(verify): env name for wiring | | `internal/wiring` does not build this context yet |

Fixed by the requirements (constants in `model`): 300 characters per SOS, 3 SOS per hour, 3 trusted contacts, 2000 characters per child card; plan sends 3 per hour.

## Invariants

- **N5 (childcare, AGENTS.md non-negotiable 5):** a childcare SOS reaches only the moderators and confirmed trusted contacts; it is never shared (service `ShareSos` → 409; the store's update has `type <> 'childcare'`; table check `sos_requests_childcare_never_shared`) and never in the feed (feed query filter; `Feed` drops it again and logs `SosFeedFilterBreach`), so nobody outside the trusted contacts can respond to it. A member becomes a trusted contact only after `Met` (on invite and on accept) and her own acceptance. Tests: `TestShareRules`, `TestFeedVisibility`, `TestChildcareNeverRespondable`, `TestFeedChildcareAndBlocksPostgres`.
- Raise is atomic: SOS, events, deliveries and timer commit together (service + store; unit and integration tests with injected failures).
- At most 3 SOS per member per rolling hour, also under concurrency (advisory lock + count; `TestRateLimitUnderConcurrencyPostgres`).
- At most 3 active trusted contacts per owner, also under concurrency (advisory lock; integration test); one active row per (owner, member) (partial unique index).
- External contacts are confirmed at add; member contacts are confirmed only by the invited member (`ConfirmTrustedContact` matches `contact_member_id`).
- Blocks hide both ways in the feed, responses, respond, accept and invitations; block lookups fail closed (`TestBlocksFailClosed`).
- The shared plan is readable only by a confirmed, active member contact after a send, and every read is audited first (service; tests).
- Sealed fields only: SOS text, child card, external name and phone (service; schema comments; `TestRaiseSosAtomicPostgres` checks with the real envelope that no plaintext is in the tables or the outbox).
- No text, name or phone in events, notifications or logs; logs carry IDs and event names (service; `newEnv` cleanup in unit tests checks logs and all event payloads).
- Nothing is deleted: SOS are closed, contacts removed with `removed_at` (no delete query exists).

## Failure modes

- Database down: every operation fails with 500; nothing is half-written (one transaction per use case); the timer worker retries with backoff (ADR-0020).
- Broker down: events wait in the outbox; the relay publishes later (ADR-0007). Moderator and contact alerts are delayed until notify receives `DeliveryRequested`.
- Sealer down: raise, card, external contacts and every read of sealed data fail; nothing is committed on writes.
- Membership down (`MemberInfo`/`Blocks`): share, feed, respond, responses, accept and contact operations fail (fail closed); raising and closing an SOS do not need membership.
- Circle down (`Acquaintances`): inviting, accepting and listing responses fail.
- A shredded member: her SOS disappear from feeds (text cannot be opened); her own reads fail as for any shredded data.

## Tests

- Unit: `service/service_test.go` with fakes from `emergencytest/` — atomic raise and rollback, idempotency, rate limit, validation and auth, close and auto-close, share rules (childcare, unverified, closed, no district), feed visibility (childcare, own, blocked both ways, district, expiry) and pagination, respond/responses/accept, trusted contact rules (unmet, blocked, duplicate, max 3, acceptance by the right member only, re-check of `Met`), notify lookups, plan send and shared-plan access and audit, fail-closed blocks, phone masking; logs and events are checked for fake personal data.
- HTTP: `api/handler_test.go` — status codes (401, 403, 409, 429), `Cache-Control: no-store`, no full phone in responses.
- Integration: `adapter/integration_test.go` with `testinfra.Postgres` — atomic raise with injected failures and the real envelope (no plaintext in tables or outbox; payloads match the `notify.DeliveryRequested` contract), concurrent rate limit and replays, childcare refused by service, store and table check, feed SQL filter, respond idempotency, auto-close timer worker, concurrent contact limit, shared-plan audit rows, schema constraints.

## Open questions

- **External one-time plan links are out of scope.** The contract promises external contacts an SMS with a 24-hour one-time link to the plan; this context requests the `emergency.plan_sent` delivery for them, but no link exists yet (needs a token table, a public endpoint and its rate limit).
- New notify templates need locale and `BIRGE_SMS_TEXTS` entries: `emergency.sos_raised`, `emergency.sos_raised_contact`, `emergency.sos_responded`, `emergency.trusted_invitation`, `emergency.plan_sent`.
- Contact alerts on raise use class `critical` (member contacts get the SMS fallback); the brief fixed the class only for the moderator alert.
- There is no endpoint for a trusted contact to read the SOS text, for moderators to see SOS in the admin, or for an invited member to list or decline invitations (she learns the `contact_id` from the invitation notification).
- The shared plan shows the owner's current card, not a snapshot of the sent one.
- notify must still wire `trusted_contact_id` resolution to `TrustedContactTarget` / `ContactPhone`, and the district push on `emergency.SosShared`.
- `MemberInfo.DisplayName` must be the first name only (contract: never address or location); this context shows what membership returns.
- Adding and removing member contacts repeatedly can send repeated invitation pushes; there is no per-hour limit on invitations yet.
