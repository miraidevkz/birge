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
| `plan_links` | One-time links of external contacts: `token_hash` (HMAC-SHA256 of the token under `Settings.PlanLinkKey`; the token is never stored), contact, owner, `sent_at`, `expires_at = sent_at + 24h`, `revoked_at`. Deleted 7 days after expiry (`emergency.PlanLinkPurge`, ADR-0023) and on account deletion | IDs and a keyed hash only |

Writes to `platform.audit_log` through the `Auditor` port (`internal/platform/audit`).

## API

Operations of tag `emergency` in `contracts/openapi/birge.yaml`. All except `viewExternalPlan` are member-only: the caller is `auth.Principal.MemberID`; staff, anonymous and sessions without a member id → 401. Another member's SOS or contact → 404 (existence is not revealed).

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
| `DELETE /me/trusted-contacts/{id}` | Owner only; sets `removed_at` (no deletion path) and revokes her plan links in the same transaction; idempotent. |
| `POST /me/trusted-invitations/{id}/accept` | Only the invited member (anyone else, or a removed invitation → 404). Re-checks `Met` (403) and blocks (404). Idempotent. |
| `POST /me/emergency-plan/send` | `confirm: SEND`; `Idempotency-Key` per member. Needs a card and ≥ 1 confirmed contact (409). At most 3 sends per hour (429: external contacts get an SMS). One transaction: `plan_sends` row, earlier plan links revoked, `plan_sent_at` on confirmed contacts, `last_sent_at`, `emergency.PlanSent`, a delivery per confirmed contact (class `normal`, template `emergency.plan_sent`, params `{owner_id}`). An external contact's link is minted later, when notify sends her SMS (`ExternalPlanLink`). |
| `POST /external/plans/view` | `security: []` (in `wiring.AnonymousOperations`); its own per-IP bucket in the wiring (`BIRGE_PLAN_LINK_RATE_PER_IP`, 429) in front of the session middleware. One lookup by keyed hash of a live link (not revoked, `expires_at > now`); unknown, expired, revoked and malformed tokens → the same 404 problem. Audit `emergency_plan.view_external` (actor = contact id, role `external_contact`, object `emergency.child_card` / owner id) in the same transaction, before the card is opened; never the token or IP. Same `SharedPlan` as `getSharedPlan`, `sentAt` = the send of the link. `Cache-Control: no-store`. The token is never logged or echoed. |
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
| consumes | `privacy.DataDeletionRequested` v1 | account deletion | JetStream durable `emergency-deletion` (`wiring.DeletionConsumer`); deduplicated in `platform.processed_events` (consumer `emergency.Deletion`) after `service.Deletion.DeleteMember`, which is idempotent and runs in one transaction |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `emergency.PlanLinkPurge` | link minted; due at `expires_at + 7 days`; subject and dedup key = link id | `HandlePlanLinkPurge` deletes the row if it expired at least 7 days ago | gone (account deletion) or not yet due → nothing; cancelled by account deletion |
| `emergency.SosAutoClose` | SOS raised; due at `closes_at` (`Settings.AutoCloseAfter`, 24 h); subject and dedup key = SOS id | `adapter.TimerHandlers.SosAutoClose` → `HandleAutoClose` in the worker's transaction: an open SOS is closed (`auto`) and `SosClosed` published; closed or unknown → nothing | cancelled when the author closes; row locked `FOR UPDATE`; the feed also hides SOS past `closes_at` if the worker is late |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `Service.SosAuthor(ctx, sosID) (uuid.UUID, err)` | Member who raised the SOS (`service.ErrNotFound` when unknown). For the SosShared district-push consumer. |
| `Service.ContactPhone(ctx, trustedContactID) (phone, lang, err)` | For notify at SMS send time: the external contact's E.164 phone (opened with the owner's key) and SMS language (`kk`/`ru`). `service.ErrNotFound` when unknown, removed or a member contact. Never log or store the phone. |
| `Service.TrustedContactTarget(ctx, id) (memberID *uuid.UUID, external bool, err)` | What a `trusted_contact_id` stands for: a confirmed member contact (`memberID`) or an external one (`external`). `service.ErrNotFound` when unknown, removed or still invited. |
| `Service.ExternalPlanLink(ctx, trustedContactID) (token, err)` | For notify (through `wiring.PlanSMSLinks`) at plan SMS send time: a new 43-character token for an active, confirmed external contact whose plan was sent less than 24 h ago; revokes her earlier links; expiry = send + 24 h. `service.ErrNotFound` otherwise (the SMS is skipped). Never log or store the token. |
| `Service.HandleAutoClose(ctx, tx, sosID)` | Timer handler; used by `adapter.TimerHandlers`. |
| `Service.HandlePlanLinkPurge(ctx, tx, linkID)` | Timer handler; used by `adapter.TimerHandlers`. |

Ports used by this context (wired later): `Sealer` (platform crypto), `MemberInfo` (membership: verification, district, display name, language), `Blocks` (membership `IsBlocked`, both directions), `Acquaintances` (circle `Met`), `Store`/`Auditor` (PostgreSQL adapter).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Settings.AutoCloseAfter` | 24h | Time after which an open SOS closes by itself (§ 4.6 «конфиг»); env `BIRGE_SOS_AUTO_CLOSE_AFTER` |
| `service.Settings.PlanLinkKey` | required | HMAC key of plan link tokens; env `BIRGE_PLAN_LINK_KEY_FILE` (api and worker) |
| `wiring.PlanSMSLinks.PublicURL` | required (worker) | link origin, `BIRGE_PUBLIC_URL`; link = `<origin>/plan#<token>` |
| `BIRGE_PLAN_LINK_RATE_PER_IP` | `10/1m` | per-IP limit of `viewExternalPlan` (wiring) |

Fixed by the requirements (constants in `model`): 300 characters per SOS, 3 SOS per hour, 3 trusted contacts, 2000 characters per child card; plan sends 3 per hour; plan links 24 h (contract), kept 7 days after expiry (ADR-0023).

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
- External plan links: the token exists only in the SMS (minted at send time, never in an event, the outbox, notify or a log); the database has only its keyed hash. Unknown, expired and revoked tokens are one `ErrNotFound` after the same single lookup (`TestExternalPlanFailuresLookAlike`). Resend, contact removal and a newer link revoke (`TestExternalPlanLinkLifecycle`, `TestExternalPlanLinkRevokedOnContactRemoval`, `TestExternalPlanLinksPostgres`, `wiring.TestExternalPlanLinkEndToEnd`, which also proves the operation is the only anonymous emergency route and greps the logs for the token).
- Nothing is deleted in normal operation: SOS are closed, contacts removed with `removed_at`. The only delete queries belong to account deletion and to the retention purge of expired plan links.
- Account deletion (AGENTS.md N6): her SOS requests with every response to them, her own responses to other members' requests, her child card, her trusted contacts, the entries in which she is another owner's member contact, her plan sends and her plan links are deleted; the auto-close timers of her requests and the purge timers of her links are cancelled, all in one transaction. Nothing is tombstoned: the other owner's contact list simply loses her, and a direct chat opened from an accepted response is kept by chat. One audit entry `emergency.member_deleted` (actor system); earlier `view_shared_plan` audit entries stay (append-only).

## Failure modes

- Database down: every operation fails with 500; nothing is half-written (one transaction per use case); the timer worker retries with backoff (ADR-0020).
- Broker down: events wait in the outbox; the relay publishes later (ADR-0007). Moderator and contact alerts are delayed until notify receives `DeliveryRequested`.
- Sealer down: raise, card, external contacts and every read of sealed data fail; nothing is committed on writes.
- Membership down (`MemberInfo`/`Blocks`): share, feed, respond, responses, accept and contact operations fail (fail closed); raising and closing an SOS do not need membership.
- Circle down (`Acquaintances`): inviting, accepting and listing responses fail.
- A shredded member: her SOS disappear from feeds (text cannot be opened); her own reads fail as for any shredded data.

## Tests

- Unit: `service/service_test.go` with fakes from `emergencytest/` — atomic raise and rollback, idempotency, rate limit, validation and auth, close and auto-close, share rules (childcare, unverified, closed, no district), feed visibility (childcare, own, blocked both ways, district, expiry) and pagination, respond/responses/accept, trusted contact rules (unmet, blocked, duplicate, max 3, acceptance by the right member only, re-check of `Met`), notify lookups, plan send and shared-plan access and audit, fail-closed blocks, phone masking; logs and events are checked for fake personal data.
- Unit (links): `service/links_test.go` — mint, view, expiry from the send, revocation on resend/removal/new link, identical failures, audit-first, rollback, purge, missing key; the cleanup greps logs and stored hashes for every minted token.
- HTTP: `api/handler_test.go` — status codes (401, 403, 409, 429), `Cache-Control: no-store`, no full phone in responses, the anonymous external view and its single 404 body.
- Integration: `adapter/integration_test.go` with `testinfra.Postgres` — atomic raise with injected failures and the real envelope (no plaintext in tables or outbox; payloads match the `notify.DeliveryRequested` contract), concurrent rate limit and replays, childcare refused by service, store and table check, feed SQL filter, respond idempotency, auto-close timer worker, concurrent contact limit, shared-plan audit rows, schema constraints.

## Open questions

- The plan link page (`apps/mobile` web route `plan`) is a separate task; until it ships the link in the SMS leads to the app's not-found page.
- A link is "one-time" in the sense of one per send and contact: it can be opened repeatedly within its 24 hours (the contract does not ask for single use).
- The dev SMS sender logs only the notification id, so in the compose stack the link cannot be read anywhere (by design).
- Contact alerts on raise use class `critical` (member contacts get the SMS fallback); the brief fixed the class only for the moderator alert.
- There is no endpoint for a trusted contact to read the SOS text, for moderators to see SOS in the admin, or for an invited member to list or decline invitations (she learns the `contact_id` from the invitation notification).
- The shared plan shows the owner's current card, not a snapshot of the sent one.
- Member trusted contacts require `Acquaintances.Met` (wired to `circle.Service.Met`); external contacts work without it.
- `MemberInfo.DisplayName` must be the first name only (contract: never address or location); the wiring adapter splits membership's display name at the first space.
- Adding and removing member contacts repeatedly can send repeated invitation pushes; there is no per-hour limit on invitations yet.
