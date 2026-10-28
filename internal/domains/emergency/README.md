# emergency

SOS and the emergency plan («SOS», «План ЧС»): a member raises an SOS that goes at once to the moderators and to her confirmed trusted contacts, may share it (verified members only, never «Не с кем оставить ребёнка») to her district feed where verified neighbours press «Могу помочь», and keeps a plan of a sealed child card and up to three trusted contacts she can send it to. External contacts (no account) open the plan and the SOS through one-time links in their SMS. Serves `docs/requirements/mvp.ru.md` § 4.6, § 3 (who may do what) and § 7 (`sos_requests`, `child_cards`, `trusted_contacts`). «Мне очень плохо» is not here: it is safety's `requestHumanHelp` with source `sos` (ADR-0023).

## Owns

| Table (schema `emergency`) | Purpose | Personal data |
|---|---|---|
| `sos_requests` | One row per SOS: type, status `open → closed` (`author`/`auto`; a moderator's close is `author` with `closed_by` = her staff ID), auto-close time, district when shared, the first moderator's `seen_at`/`seen_by`; `(member_id, idempotency_key)` unique | `content` (the text) sealed with the author's key (AD `emergency.sos.text\|<id>`); `member_id` pseudonymous |
| `sos_responses` | «Могу помочь», one per responder and SOS; `accepted_at` when the author chose her | IDs only |
| `child_cards` | One child card per member; `last_sent_at` | `content` sealed with the member's key (AD `emergency.child_cards.text\|<member id>`) |
| `trusted_contacts` | Up to 3 active contacts per owner: `member` (invited → confirmed) or `external` (confirmed at once); `plan_sent_at`; removed with `removed_at`, never deleted | external `name` and `phone` sealed with the owner's key (AD `emergency.trusted_contacts.name\|<id>`, `….phone\|<id>`); `lang` |
| `plan_sends` | One row per confirmed «Отправить план» (Idempotency-Key); also the plan-send rate limit | IDs and counts only |
| `sos_links` | Links of external contacts to one SOS: `token_hash` (HMAC-SHA256 of `"emergency.sos_link|" + token` under `Settings.PlanLinkKey`, so it never equals a plan link hash; the token is never stored), SOS, contact, owner (= the SOS author), `expires_at = least(closes_at, SOS created_at + 24h)`, `revoked_at` (a newer link for the same SOS and contact). Its own table, not a kind column on `plan_links`: different lifetime and revocation, and no plan query can forget a kind filter. Deleted 7 days after expiry (`emergency.SosLinkPurge`) and on account deletion | IDs and a keyed hash only |
| `plan_links` | One-time links of external contacts: `token_hash` (HMAC-SHA256 of the token under `Settings.PlanLinkKey`; the token is never stored), contact, owner, `sent_at`, `expires_at = sent_at + 24h`, `revoked_at`. Deleted 7 days after expiry (`emergency.PlanLinkPurge`, ADR-0023) and on account deletion | IDs and a keyed hash only |

Writes to `platform.audit_log` through the `Auditor` port (`internal/platform/audit`).

## API

Operations of tag `emergency` in `contracts/openapi/birge.yaml`. All except `viewExternalPlan`, `viewExternalSos` and the three `/staff/sos` operations are member-only: the caller is `auth.Principal.MemberID`; staff, anonymous and sessions without a member id → 401. Another member's SOS or contact → 404 (existence is not revealed). The `/staff/sos` operations live under `/v1/staff/`, so the wiring puts them behind staffauth (OIDC, MFA; 503 when no provider is configured; `wiring.TestStaffSosRoutesBehindStaffAuth`); the service checks again: anonymous → 401, members, staff without MFA and roles other than `moderator`/`admin` → 403.

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
| `GET /me/trusted-invitations/{id}` | Only the invited member, while the entry is active; anyone else, a removed entry or an owner blocked either way → 404. Status `invited` or `confirmed`, `invitedAt` = when the owner added her, the owner's first name. |
| `POST /me/trusted-invitations/{id}/decline` | Only the invited member (anyone else → 404); also ends a confirmed entry. Same effect as the owner's removal: `removed_at` and her plan links revoked in one transaction (a link minted concurrently is still refused, because the link view re-checks `removed_at`). No event, no notification, no reason stored. Repeats → 204. |
| `GET /trusted/sos/{id}` | Unknown SOS → 404. Only an active, confirmed member contact of the author (`GetSharedPlanContact`), any type, childcare included, open or closed; the author herself, invited, removed, external entries and members blocked either way → 403 without saying why. Audit `emergency_sos.view_trusted` (actor = member, object `emergency.sos` / SOS id) in the same transaction as the access check, before the text is opened. `Cache-Control: no-store`. `respondedByMe` from `sos_responses`. |
| `POST /trusted/sos/{id}/respond` | Same access as above (404, then 403), checked under the SOS row lock. The same `sos_responses` row as `respondToSos`, so the author lists and accepts it as any response; the first one notifies her as `respondToSos` does. Closed → 409, unless she already responded (a retry → 204). Idempotent per responder. |
| `GET /staff/sos` | `status` `open` (default; oldest first) or `closed` (newest first), every type including childcare, 50 per page, keyset cursor. Item: text, response count, seen/closed fields, the author's first name and her district from membership (her profile district, not the shared one); never a phone. One audit entry `emergency_sos.view_staff` per item shown, in the listing transaction, before any text is opened, unless the same staff member already has one for that SOS from the last 10 minutes (`model.StaffViewAuditDedup`; one batched lookup per page on the audit log's own clock, `audit.RecentObjects`): the admin polls every 30 s. A failed lookup fails the listing (nothing is shown unaudited). A text that cannot be opened is shown empty rather than hidden. `Cache-Control: no-store`. |
| `POST /staff/sos/{id}/seen` | Unknown → 404. The first staff ID and time are kept. Audit `emergency_sos.seen` the first time, `emergency_sos.view_staff` on a repeat. Closed SOS may be marked. |
| `POST /staff/sos/{id}/close` | Unknown → 404. The author's close with the staff actor: status `closed` (`close_reason` `author`, `closed_by` = staff ID), auto-close timer cancelled, out of the feed, `emergency.SosClosed` and audit `emergency_sos.close_staff`, in one transaction. A closed SOS → 200 unchanged, audited `emergency_sos.view_staff`. The author is not notified. |
| `POST /me/trusted-invitations/{id}/accept` | Only the invited member (anyone else, or a removed invitation → 404). Re-checks `Met` (403) and blocks (404). Idempotent. |
| `POST /me/emergency-plan/send` | `confirm: SEND`; `Idempotency-Key` per member. Needs a card and ≥ 1 confirmed contact (409). At most 3 sends per hour (429: external contacts get an SMS). One transaction: `plan_sends` row, earlier plan links revoked, `plan_sent_at` on confirmed contacts, `last_sent_at`, `emergency.PlanSent`, a delivery per confirmed contact (class `normal`, template `emergency.plan_sent`, params `{owner_id}`). An external contact's link is minted later, when notify sends her SMS (`ExternalPlanLink`). |
| `POST /external/plans/view` | `security: []` (in `wiring.AnonymousOperations`); its own per-IP bucket in the wiring (`BIRGE_PLAN_LINK_RATE_PER_IP`, 429) in front of the session middleware. One lookup by keyed hash of a live link (not revoked, `expires_at > now`); unknown, expired, revoked and malformed tokens → the same 404 problem. Audit `emergency_plan.view_external` (actor = contact id, role `external_contact`, object `emergency.child_card` / owner id) in the same transaction, before the card is opened; never the token or IP. Same `SharedPlan` as `getSharedPlan`, `sentAt` = the send of the link. `Cache-Control: no-store`. The token is never logged or echoed. |
| `POST /external/sos/view` | `security: []` (in `wiring.AnonymousOperations`); a per-IP bucket of its own (`sos_link.ip`, same `BIRGE_PLAN_LINK_RATE_PER_IP` rate, 429) in front of the session middleware. One lookup by keyed hash that also re-checks the contact (active, external, confirmed, the author's) and the SOS (open, before `closes_at`); unknown, expired, revoked, closed, removed and malformed → the same 404 problem. Audit `emergency_sos.view_external` (actor = contact id, role `external_contact`, object `emergency.sos` / SOS id) in the same transaction, before the text is opened; never the token or IP. Every type, childcare included (she is one of the mother's trusted contacts). `ExternalSos`: first name, type, text, `createdAt`, `status` (always `open`, a closed one is 404), `closesAt`. `Cache-Control: no-store`. The token is never logged or echoed. |
| `GET /trusted/plans/{ownerId}` | Only an active, confirmed member contact of that owner whose `plan_sent_at` is set; anything else → 403 without saying why. Audit `emergency_plan.view_shared` (actor = member, object `emergency.child_card` / owner id) is written in the same transaction as the access check, before the card is opened; a failed audit returns nothing. `Cache-Control: no-store`. Shows the owner's current card. |

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `emergency.SosRaised` v1 | new SOS (not on replay) | outbox, same transaction as the insert, alerts and timer |
| publishes | `notify.DeliveryRequested` v1 | SOS raised (moderators: `staff_role=moderator`, `emergency.sos_raised`; contacts: `member_id` or `trusted_contact_id`, `emergency.sos_raised_contact`; class `critical`, params `{sos_id}`: an external contact's SMS gets its SOS link from that id at send time, or the text without the link if none can be made); first response (from the feed or from a trusted contact); member invitation; plan sent | outbox, same transaction; `request_id` = SHA-1 UUID of subject + template + recipient, so retries deduplicate in notify; template keys and IDs only |
| publishes | `emergency.SosShared` v1 | first share | outbox, same transaction |
| publishes | `emergency.SosClosed` v1 | close by the author, by a moderator or by the timer | outbox, same transaction |
| publishes | `emergency.SosResponseAccepted` v1 | first acceptance of a response | outbox, same transaction |
| publishes | `emergency.PlanSent` v1 | plan sent (not on replay) | outbox, same transaction |
| consumes | `privacy.DataDeletionRequested` v1 | account deletion | JetStream durable `emergency-deletion` (`wiring.DeletionConsumer`); deduplicated in `platform.processed_events` (consumer `emergency.Deletion`) after `service.Deletion.DeleteMember`, which is idempotent and runs in one transaction |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `emergency.PlanLinkPurge` | link minted; due at `expires_at + 7 days`; subject and dedup key = link id | `HandlePlanLinkPurge` deletes the row if it expired at least 7 days ago | gone (account deletion) or not yet due → nothing; cancelled by account deletion |
| `emergency.SosLinkPurge` | SOS link minted; due at `expires_at + 7 days`; subject and dedup key = link id | `HandleSosLinkPurge` deletes the row if it expired at least 7 days ago | gone (account deletion) or not yet due → nothing; cancelled by account deletion |
| `emergency.SosAutoClose` | SOS raised; due at `closes_at` (`Settings.AutoCloseAfter`, 24 h); subject and dedup key = SOS id | `adapter.TimerHandlers.SosAutoClose` → `HandleAutoClose` in the worker's transaction: an open SOS is closed (`auto`) and `SosClosed` published; closed or unknown → nothing | cancelled when the author or a moderator closes; row locked `FOR UPDATE`; the feed also hides SOS past `closes_at` if the worker is late |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `Service.SosAuthor(ctx, sosID) (uuid.UUID, err)` | Member who raised the SOS (`service.ErrNotFound` when unknown). For the SosShared district-push consumer. |
| `Service.ContactPhone(ctx, trustedContactID) (phone, lang, err)` | For notify at SMS send time: the external contact's E.164 phone (opened with the owner's key) and SMS language (`kk`/`ru`). `service.ErrNotFound` when unknown, removed or a member contact. Never log or store the phone. |
| `Service.TrustedContactTarget(ctx, id) (memberID *uuid.UUID, external bool, err)` | What a `trusted_contact_id` stands for: a confirmed member contact (`memberID`) or an external one (`external`). `service.ErrNotFound` when unknown, removed or still invited. |
| `Service.ExternalPlanLink(ctx, trustedContactID) (token, err)` | For notify (through `wiring.EmergencySMSLinks`) at plan SMS send time: a new 43-character token for an active, confirmed external contact whose plan was sent less than 24 h ago; revokes her earlier links; expiry = send + 24 h. `service.ErrNotFound` otherwise (the SMS is skipped). Never log or store the token. |
| `Service.ExternalSosLink(ctx, trustedContactID, sosID) (token, err)` | For notify (through `wiring.EmergencySMSLinks`, `sos_id` from the notification's params) at SOS SMS send time: a new 43-character token for an active, confirmed external contact of the author of an open SOS less than 24 h old; revokes her earlier link for that SOS; expiry = the earlier of auto-close and raise + 24 h. `service.ErrNotFound` otherwise (notify then sends the SMS without the link, never not at all). Never log or store the token. |
| `Service.HandleAutoClose(ctx, tx, sosID)` | Timer handler; used by `adapter.TimerHandlers`. |
| `Service.HandlePlanLinkPurge(ctx, tx, linkID)` | Timer handler; used by `adapter.TimerHandlers`. |
| `Service.HandleSosLinkPurge(ctx, tx, linkID)` | Timer handler; used by `adapter.TimerHandlers`. |

Ports used by this context (wired later): `Sealer` (platform crypto), `MemberInfo` (membership: verification, district, display name, language), `Blocks` (membership `IsBlocked`, both directions), `Acquaintances` (circle `Met`), `Store`/`Auditor` (PostgreSQL adapter).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Settings.AutoCloseAfter` | 24h | Time after which an open SOS closes by itself (§ 4.6 «конфиг»); env `BIRGE_SOS_AUTO_CLOSE_AFTER` |
| `service.Settings.PlanLinkKey` | required | HMAC key of plan and SOS link tokens (SOS under the prefix `emergency.sos_link|`); env `BIRGE_PLAN_LINK_KEY_FILE` (api and worker) |
| `wiring.EmergencySMSLinks.PublicURL` | required (worker) | link origin, `BIRGE_PUBLIC_URL`; links = `<origin>/plan#<token>` and `<origin>/sos-link#<token>` |
| `BIRGE_PLAN_LINK_RATE_PER_IP` | `10/1m` | per-IP limit of `viewExternalPlan` and, in a separate bucket, `viewExternalSos` (wiring) |

Fixed by the requirements (constants in `model`): 300 characters per SOS, 3 SOS per hour, 3 trusted contacts, 2000 characters per child card; plan sends 3 per hour; plan links 24 h (contract), SOS links until the SOS closes and at most 24 h, both kept 7 days after expiry (ADR-0023); a staff view audit covers 10 minutes.

## Invariants

- **N5 (childcare, AGENTS.md non-negotiable 5):** a childcare SOS reaches only the moderators and confirmed trusted contacts (they read and answer it through `/trusted/sos`, which admits no one else: `TestTrustedSosAccess`); it is never shared (service `ShareSos` → 409; the store's update has `type <> 'childcare'`; table check `sos_requests_childcare_never_shared`) and never in the feed (feed query filter; `Feed` drops it again and logs `SosFeedFilterBreach`), so nobody outside the trusted contacts can respond to it. A member becomes a trusted contact only after `Met` (on invite and on accept) and her own acceptance. Tests: `TestShareRules`, `TestFeedVisibility`, `TestChildcareNeverRespondable`, `TestFeedChildcareAndBlocksPostgres`.
- Raise is atomic: SOS, events, deliveries and timer commit together (service + store; unit and integration tests with injected failures).
- At most 3 SOS per member per rolling hour, also under concurrency (advisory lock + count; `TestRateLimitUnderConcurrencyPostgres`).
- At most 3 active trusted contacts per owner, also under concurrency (advisory lock; integration test); one active row per (owner, member) (partial unique index).
- External contacts are confirmed at add; member contacts are confirmed only by the invited member (`ConfirmTrustedContact` matches `contact_member_id`).
- Blocks hide both ways in the feed, responses, respond, accept and invitations; block lookups fail closed (`TestBlocksFailClosed`).
- The shared plan is readable only by a confirmed, active member contact after a send, and every read is audited first (service; tests).
- A trusted SOS is readable only by a confirmed, active member contact of the author, and every read is audited first (`TestTrustedSosAccess`, `TestTrustedSosPostgres`). Declining is the owner's removal, including plan-link revocation, atomically (`TestDeclineByConfirmedContactEndsAccessAndRevokesLinks`).
- The SOS queue is deny by default (role and MFA in the service, staffauth on the route) and every SOS shown to staff, seen or closed by staff is audited in the same transaction (`TestStaffSosDenyByDefault`, `TestStaffSeenAndClose`, `TestStaffSosQueuePostgres`). The first "Seen" is kept (`MarkSosSeen` updates only `seen_at IS NULL`); `sos_requests_seen_check` and `sos_requests_closed_by_check` guard the columns.
- Sealed fields only: SOS text, child card, external name and phone (service; schema comments; `TestRaiseSosAtomicPostgres` checks with the real envelope that no plaintext is in the tables or the outbox).
- No text, name or phone in events, notifications or logs; logs carry IDs and event names (service; `newEnv` cleanup in unit tests checks logs and all event payloads).
- External plan links: the token exists only in the SMS (minted at send time, never in an event, the outbox, notify or a log); the database has only its keyed hash. Unknown, expired and revoked tokens are one `ErrNotFound` after the same single lookup (`TestExternalPlanFailuresLookAlike`). Resend, contact removal and a newer link revoke (`TestExternalPlanLinkLifecycle`, `TestExternalPlanLinkRevokedOnContactRemoval`, `TestExternalPlanLinksPostgres`, `wiring.TestExternalPlanLinkEndToEnd`, which also proves the operation is the only anonymous emergency route and greps the logs for the token).
- External SOS links: the same token rules as plan links (only in the SMS; keyed hash with its own prefix, so plan and SOS tokens never cross: `TestPlanAndSosTokensNeverCross`). One lookup decides and re-checks the contact and the SOS, so a closed SOS, a removed contact and an unknown, revoked or expired token are one `ErrNotFound` (`TestExternalSosFailuresLookAlike`, `TestExternalSosLinkLifecycle`, `TestExternalSosLinkEndsWithAutoClose`, `TestExternalSosLinkEndsOnContactRemoval`, `TestExternalSosLinksPostgres`, `wiring.TestExternalSosLinkEndToEnd`). The SOS SMS never waits for its link (notify's critical fallback).
- Nothing is deleted in normal operation: SOS are closed, contacts removed with `removed_at`. The only delete queries belong to account deletion and to the retention purge of expired plan and SOS links.
- Account deletion (AGENTS.md N6): her SOS requests with every response to them, her own responses to other members' requests, her child card, her trusted contacts, the entries in which she is another owner's member contact, her plan sends and her plan and SOS links are deleted; the auto-close timers of her requests and the purge timers of her links are cancelled, all in one transaction. Nothing is tombstoned: the other owner's contact list simply loses her, and a direct chat opened from an accepted response is kept by chat. One audit entry `emergency.member_deleted` (actor system); earlier audit entries about her (shared-plan views, trusted and staff SOS reads and actions; IDs only) stay (append-only).

## Failure modes

- Database down: every operation fails with 500; nothing is half-written (one transaction per use case); the timer worker retries with backoff (ADR-0020).
- Broker down: events wait in the outbox; the relay publishes later (ADR-0007). Moderator and contact alerts are delayed until notify receives `DeliveryRequested`.
- Sealer down: raise, card, external contacts and every read of sealed data fail; nothing is committed on writes.
- Membership down (`MemberInfo`/`Blocks`): share, feed, respond, responses, accept and contact operations fail (fail closed); raising and closing an SOS do not need membership.
- Circle down (`Acquaintances`): inviting, accepting and listing responses fail.
- A shredded member: her SOS disappear from feeds (text cannot be opened); her own reads fail as for any shredded data.

## Tests

- Unit: `service/service_test.go` with fakes from `emergencytest/` — atomic raise and rollback, idempotency, rate limit, validation and auth, close and auto-close, share rules (childcare, unverified, closed, no district), feed visibility (childcare, own, blocked both ways, district, expiry) and pagination, respond/responses/accept, trusted contact rules (unmet, blocked, duplicate, max 3, acceptance by the right member only, re-check of `Met`), notify lookups, plan send and shared-plan access and audit, fail-closed blocks, phone masking; logs and events are checked for fake personal data.
- Unit (links): `service/links_test.go` — mint, view, expiry from the send, revocation on resend/removal/new link, identical failures, audit-first, rollback, purge, missing key; the cleanup greps logs and stored hashes for every minted token. `service/sos_links_test.go` — the same for SOS links (expiry at auto-close or 24 h, close and removal end the link, plan and SOS tokens never cross) and the staff view audit dedup (`TestStaffSosViewAuditDeduplicated`).
- Unit (trusted and staff): `service/trusted_staff_test.go` — trusted read and respond deny by default (anonymous, staff, the author, non-contact, invited, unknown, blocked, removed), childcare visible to the contact, audit-first, respond idempotent, notification, on a closed SOS, rollback; invitation read and decline by the right member only, idempotent, no event, plan-link revocation in the same transaction; staff deny by default (anonymous, member, no MFA, psychologist, lawyer, no role, no staff flag), queue order, paging and per-item audit, seen kept first, close effects, idempotency, rollback and audit.
- HTTP: `api/handler_test.go`, `api/trusted_staff_test.go` — status codes (401, 403, 404, 409, 429), `Cache-Control: no-store`, no full phone in responses, the anonymous external view and its single 404 body.
- Integration: `adapter/staff_trusted_integration_test.go` — trusted read, respond and decline; staff queue paging, seen, close with timer, event and audit rows, the new checks. `wiring/staff_sos_e2e_test.go` — `/v1/staff/sos` behind staffauth through the real HTTP stack.
- Integration: `adapter/integration_test.go` with `testinfra.Postgres` — atomic raise with injected failures and the real envelope (no plaintext in tables or outbox; payloads match the `notify.DeliveryRequested` contract), concurrent rate limit and replays, childcare refused by service, store and table check, feed SQL filter, respond idempotency, auto-close timer worker, concurrent contact limit, shared-plan audit rows, schema constraints.

## Open questions

- A link is "one-time" in the sense of one per send and contact: it can be opened repeatedly within its 24 hours (the contract does not ask for single use).
- The dev SMS sender logs only the notification id, so in the compose stack the link cannot be read anywhere (by design).
- The SOS link page is `/sos-link`, not `/sos`: `/sos` is the mother's own SOS screen (signed in or out), and a page that changed with the URL fragment would put an anonymous page and the mother's crisis entry behind one route.
- An external contact sees the SOS but cannot answer it in the system (she has no account); the page asks her to get in touch the way she usually does.
- Contact alerts on raise use class `critical` (member contacts get the SMS fallback); the brief fixed the class only for the moderator alert.
- There is no endpoint for an invited member to list her invitations: she learns the `contact_id` from the invitation notification and reads it with `getTrustedInvitation`.
- A moderator's close is stored as `close_reason = 'author'` with `closed_by` set, because widening the reason check to `staff` is a constraint swap the migration gate treats as destructive. If a separate reason is wanted, it needs a `-- birge:destructive-approved` migration.
- A staff close does not notify the author; the contract does not ask for it.
- The shared plan shows the owner's current card, not a snapshot of the sent one.
- Member trusted contacts require `Acquaintances.Met` (wired to `circle.Service.Met`); external contacts work without it.
- `MemberInfo.DisplayName` must be the first name only (contract: never address or location); the wiring adapter splits membership's display name at the first space.
- Adding and removing member contacts repeatedly can send repeated invitation pushes; there is no per-hour limit on invitations yet.
