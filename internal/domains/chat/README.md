# chat

**In short:** The chat rooms: one per district, one per topic, and private rooms that open when two people agree to talk. Messages are moderated on the server and never end-to-end encrypted, so harmful content can be stopped.

In-app chats (ADR-0016, `docs/requirements/mvp.md` § 4.4 «Chats»): district rooms and topic rooms (by special-needs type) that verified members join and leave themselves, and private rooms opened after a mutual tea match, after the mother accepted an SOS responder, or after a listing author accepted a contact request. Every message passes the safety crisis lexicon before it can be seen; filters and newcomer holds feed moderation; blocks hide members from each other both ways (ADR-0019). Text only. Delivery is a WebSocket while the app is open and a content-free push otherwise.

## Owns

| Table (schema `chat`) | Purpose | Personal data |
|---|---|---|
| `rooms` | District room (one per membership district id), topic room (one per needs code), direct room (one per origin `circle.match` / `emergency.sos` / `sharing.listing`, origin id and ordered pair); `slow_mode_seconds`; `last_seq`, the per-room sequence counter | IDs and locale keys only |
| `room_members` | Membership (`left_at` when she leaves; deleted only by account deletion), push mute, `last_push_at` (throttle), `connected_until` (gateway presence) | IDs only |
| `room_restrictions` | Ban (`banned_at`) and `mute_1h` (`muted_until`) per room and member | IDs only |
| `room_mentors` | Mentors assigned to group rooms by staff (`removed_at`, also set on every active row when membership takes the mentor role) | IDs only |
| `messages` | Message with `seq` (unique per room), `client_id` (unique per author), status `visible`/`held`/`hidden`, `hold_reason` `crisis`/`newcomer`/`link`/`phone`, the crisis `escalation_id`, `legal_hold`, `purged_at` | `body`: the text sealed with the **author's** key (AD `chat.messages.text\|<message id>`); NULL once purged |
| `reports` | One report per reporter and message; `closed_at` | IDs and the reason code |

Uses `platform.processed_events` (consumers `chat.DirectRoomOpener`, `chat.moderation.Decided`, `chat.membership.MentorRoleChanged`), `platform.outbox`, `platform.timers` and `platform.audit_log`.

## API

Operations of tag `chat` in `contracts/openapi/birge.yaml`, plus the WebSocket `GET /v1/chat/ws` (`contracts/realtime/chat.v1.md`, package `gateway`). Members only: the caller is `auth.Principal.MemberID` (staff, anonymous and `uuid.Nil` → 401), except `moderateRoom` and the staff mentor / legal-hold routes.

| Operation | Rules not in the contract |
|---|---|
| `GET /chat/rooms` | `joined=true`: her rooms (direct-room title = the other member's first name); `joined=false`: group rooms she may join — only when verified: her district's room and every topic room; unset: both. |
| `POST /chat/rooms/{id}/join` | Group rooms: verified only (403); a district room only of her own district (403); banned → 403 `banned`. Direct rooms: only their two members (404 otherwise). Idempotent. |
| `POST /chat/rooms/{id}/leave` | Idempotent; ends her live subscriptions (a `left` notice). |
| `PUT /chat/rooms/{id}/mute` | Push mute only; not in the room → 403 `not_member`. |
| `GET /chat/rooms/{id}/messages` | Members of the room only (403 `not_member`). `after` and `before` are exclusive of each other. Held messages of others and messages of members blocked either way are left out; the page is filled from further rows, so a page shorter than `limit` means the end. Purged or shredded texts read as `deleted`. |
| `POST /chat/rooms/{id}/messages` | See *Sending*. 429 `rate_limited` / `slow_mode`; 403 `banned` (ban or `mute_1h`) / `not_member`. |
| `POST /chat/messages/{id}/report` | Only a message she can see (404 otherwise, also her own → 400). One per reporter and message; the first publishes `chat.MessageReported`. |
| `POST /chat/rooms/{id}/moderation` | Staff with role `moderator` or `admin`: every action. A member who holds the membership role `mentor` **and** is assigned to this room: `hide_message`, `restore_message`, `mute_1h` only. Anyone else 403. `ban` also removes her from the room; a crisis-held message is never restored (409). Every applied action writes `platform.audit_log` in the same transaction (actor role `staff` or `mentor`; object `chat.message`, `chat.member` or `chat.room`). |
| `GET /staff/chat/rooms` | Staff `moderator`/`admin`, MFA. District and topic rooms with member counts and current mentors (first name from membership at request time); direct rooms are never listed. A read: logged by event name and staff ID, not audited. |
| `PUT /staff/chat/rooms/{id}/mentors/{memberId}` | Staff `moderator`/`admin`, MFA. Member must hold the mentor role (409 otherwise); group rooms only (409). Audited `chat.room.mentor_assign`. |
| `DELETE /staff/chat/rooms/{id}/mentors/{memberId}` | Staff `moderator`/`admin`, MFA. Audited `chat.room.mentor_remove`. |
| `PUT /staff/chat/messages/{id}/legal-hold` | Staff `moderator`/`admin`, MFA. Audited `chat.message.legal_hold_on`. |
| `DELETE /staff/chat/messages/{id}/legal-hold` | Staff `moderator`/`admin`, MFA. Audited `chat.message.legal_hold_off`. |
| `GET /v1/chat/ws` | First frame `auth` within 10 s, else close 4401. `subscribe` replays every visible message after `afterSeq`, then live. `send` = the HTTP send; the `crisis` frame comes before the `ack`. Error frame codes: `rate_limited`, `slow_mode`, `banned`, `not_member` (also unknown room), `invalid`; an unexpected failure closes with 1011 (the client retries the same `clientId`). Slow consumer → close 1013. Read limit 16 KiB, idle timeout 90 s, write timeout 10 s, bounded queue of 256 live items. |

### Sending

1. Validate (`clientId` a UUIDv7; text 1..2000 characters, not blank, UTF-8).
2. **Crisis lexicon** (safety, local, before any network call; every loaded language is checked). A hit: the message is stored `held` (reason `crisis`; never shown to others, never published to moderation, no live fan-out notice — the author's other devices see it on their next catch-up), bypassing rate limit, slow mode, ban and mute; the author gets the crisis notice (`crisis` frame: crisis phones from safety's configuration and the escalation id); safety `OpenEscalation(member, "chat")` runs right after commit, and a `chat.CrisisEscalation` timer created in the insert transaction retries it. If the message cannot be stored (not a member, database down) the notice is still returned and the escalation attempted directly.
3. Otherwise: ban / `mute_1h` → `banned`; more than `RateLimit` messages in `RateWindow` across all rooms → `rate_limited`; slow mode → `slow_mode`.
4. Holds, published as `chat.MessageHeld`: a link → `link`, a phone-like number (≥ 10 digits) → `phone`, a newcomer's first `NewcomerHeld` group-room messages → `newcomer` (direct rooms have no newcomer hold).
5. One transaction: the author's advisory lock, idempotency check, `NextSeq` (room row lock), insert, event or timer. A repeated `clientId` returns the stored message (`Replayed`) and changes nothing.

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `chat.MessageHeld` v1 | message held for `newcomer`, `link` or `phone` (never `crisis`) | outbox, same transaction as the insert |
| publishes | `chat.MessageReported` v1 | first report of a message by a member | outbox, same transaction as the report |
| publishes | `notify.DeliveryRequested` v1 | `chat.PushFanout` timer: one per eligible member (class `normal`, template `chat.new_message`, params `{room_id}`) | outbox, the timer's transaction; `request_id` = SHA-1 UUID of message + template + member |
| consumes | `circle.MatchAccepted` v1, `emergency.SosResponseAccepted` v1, `sharing.ContactAccepted` v1 | open the direct room of the pair | JetStream durable consumer `chat-direct-rooms`; deduplicated by event id in `platform.processed_events` and by the room's natural key; needs only the chat database |
| consumes | `moderation.Decided` v1 | kinds `chat_message` and `report` only | JetStream durable `chat-moderation-decided` (filter `birge.events.moderation.Decided`); deduplicated as `chat.moderation.Decided`; object gone → no-op; audits as role `system` with ActorID = moderation item id |
| consumes | `membership.MentorRoleChanged` v1 | `mentor: false` → sets `removed_at` on every active `room_mentors` row of the member; `mentor: true` → nothing (assignment stays a staff action per room) | JetStream durable `chat-mentor-role-changed`; deduplicated as `chat.membership.MentorRoleChanged` in the removal transaction (a replay or a second revoke ends nothing); one audit row `chat.room.mentor_remove` per room ended, role `system`, ActorID = event id, object `chat.member`, reason `membership.mentor_revoke`; needs only the chat database |
| consumes | `privacy.DataDeletionRequested` v1 | account deletion | JetStream durable `chat-deletion` (`wiring.DeletionConsumer`); deduplicated in `platform.processed_events` (consumer `chat.Deletion`) after `service.Deletion.DeleteMember`, which is idempotent and runs in one transaction |

`moderation.Decided` application:

| Kind | Approve | Reject |
|---|---|---|
| `chat_message` | held → `visible` (never restores a crisis hold); schedule push + fan-out when leaving held | → `hidden` |
| `report` | `CloseReports` (dismiss) | → `hidden` + `CloseReports` |

Also, when set on the decision: `legal_hold` → `SetLegalHold(true)`; `block_author` → `AuthorSuspender.SuspendAuthor` (membership; required when the flag is set — failure rolls back the chat transaction so the event is redelivered; a deleted author is a no-op in membership, so it never redelivers forever).

Fan-out (not domain events): NATS **core** subjects `birge.chat.room.<room id>` carry notices `{kind: new|changed|left, room_id, message_id?, seq?, member_id?}` — IDs only, published after commit, best-effort.

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `chat.CrisisEscalation` | crisis message stored; due 30 s later; cancelled when the synchronous escalation succeeded | opens the escalation if the message has none and records it | message row locked; `OpenEscalation` idempotent per member; a failure retries with backoff |
| `chat.PushFanout` | visible message stored, or a held message restored | pushes to members who are in the room, not the author, not muted, not connected (`connected_until`), not pushed for the room within `PushInterval`, not blocked with the author | deterministic request ids; `last_push_at` |
| `chat.RetentionPurge` | `EnsureRetentionTimer` (worker start), then by each run for the next day at `RetentionAt` UTC; dedup key `retention\|<date>` | clears texts of group messages older than `RetentionGroup` and direct messages older than `RetentionDirect`, except under legal hold or an open report; the row stays as a `deleted` tombstone | batches of 1000 with `SKIP LOCKED`; re-running purges nothing twice |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `EnsureDistrictRoom(ctx, districtID, titleKey)` / `EnsureTopicRoom(ctx, code, titleKey)` | One room per district / needs code, also under concurrency (partial unique indexes `rooms_district_uidx`, `rooms_topic_uidx` + `ON CONFLICT DO NOTHING`); returns the existing one, its id and title unchanged. |
| `EnsureGroupRooms(ctx, districts, topics)` | The same for a list of active districts and needs codes, returning how many rooms it created. `birge migrate` calls it on every run after the membership reference sync (`wiring.ensureChatRooms`), so every active district and needs code has its room on fresh and existing databases. Only adds: the room of a district or code no longer passed (deactivated) stays with its members and history. Chat never reads membership tables; wiring passes the ids and codes. |
| `HandleRoomEvent(ctx, eventID, type, data)` / `OpenDirectRoom(...)` | One direct room per origin, origin id and pair; both members added once. |
| `HandleCrisisEscalation`, `HandlePush`, `HandleRetention` | Timer handlers (`adapter.TimerHandlers`). `EnsureRetentionTimer` must be called once at worker start. |
| `ApplyModerationDecided(ctx, eventID, data)` | Applies `moderation.Decided` for `chat_message` / `report` (`adapter.ModerationConsumer`). |
| `ModerationSummary(ctx, messageID)` | Opened message text for moderation's `ContentSource` (hidden still returns text; purged/gone → `ErrNotFound`; no contacts). |
| `ApplyMentorRoleChanged(ctx, eventID, data)` | Applies `membership.MentorRoleChanged` (`adapter.MentorRoleConsumer`); idempotent per event id. |
| `CloseReports(ctx, staff, messageID)`, `SetLegalHold(ctx, staff, messageID, on)` | Staff (`moderator`/`admin`), audited; also via staff HTTP legal-hold routes. |
| `AssignMentor(ctx, staff, roomID, memberID, assign)` | Staff, audited; the member must hold the mentor role; also via staff HTTP mentor routes. |
| `StaffListRooms(ctx, staff)` | Staff (`moderator`/`admin`); group rooms with counts and mentors for the admin app. |

Ports used (wired in `internal/wiring`): `Sealer` (platform crypto), `MemberInfo` (membership: verified, first name, language, district), `Blocks` (membership `IsBlocked`), `Mentors` (membership role `mentor`), `AuthorSuspender` (membership suspend for `block_author`), `Crisis` (safety `Detect`, `CrisisPhones`, `OpenEscalation`), `Fanout`/`Bus` (`adapter.NATSFanout`), `Authenticator` (session validator), `Store` (`adapter.PostgresStore`).

## Configuration

| Variable (`service.Settings`) | Default | Meaning |
|---|---|---|
| `RetentionGroup` | 365 days | Text retention in district and topic rooms (ADR-0016) |
| `RetentionDirect` | 90 days | Text retention in direct rooms (ADR-0016) |
| `RetentionAt` | 03:00 UTC | Daily retention run |
| `NewcomerHeld` | 3 | First group-room messages of a member that are held (negative: off) |
| `RateLimit` / `RateWindow` | 20 / 1 min | Messages per member across all rooms |
| `PushInterval` | 10 min | At most one push per room per member per interval |
| `PresenceTTL` | 90 s | A gateway heartbeat (every TTL/3) counts as connected for this long |

Gateway (`gateway.Options`): `QueueSize` 256, `AuthTimeout` 10 s, `IdleTimeout` 90 s, `WriteTimeout` 10 s, `OriginPatterns` (web origins; native apps send none). Fixed by the contract: 2000 characters, pages of 1..100 (default 50), slow mode 0..3600 s, mute 1 h.

## Invariants

- **Sequence:** per room strictly increasing and gap-free in committed rows: `last_seq` is incremented by `UPDATE … RETURNING` in the insert transaction, whose row lock serialises senders and whose rollback returns the number (a `SEQUENCE` would leave gaps). Lock order: author advisory lock, then room row. `UNIQUE (room_id, seq)`. Test: `TestSeqUnderConcurrencyPostgres` (50 concurrent senders → 1..50).
- **Idempotent send:** `UNIQUE (author_id, client_id)`; the existence check runs under the author's lock; a concurrent duplicate in another room rolls back (no sequence gap). `TestIdempotentSendPostgres`.
- **Crisis first:** the lexicon runs before filters, limits and any membership call; crisis messages are never visible to others, never in `chat.MessageHeld`, never restorable; the escalation has a durable retry. `TestCrisisMessageHeldEscalatedPostgres`, `TestHappyPathAcrossReplicas`.
- **Blocks:** messages of members blocked either way are never listed, rendered for the WebSocket, reportable or pushed; lookups fail closed. `TestBlockedFilteringBothDirectionsPostgres`.
- **Moderation:** staff all actions, mentors only hide/restore/mute_1h in rooms they are assigned to and only while holding the role (checked live at every action; the assignments themselves end when `membership.MentorRoleChanged` `mentor: false` arrives); every applied action audited. `TestMentorLimitsPostgres`, `TestMentorRoleRevokedConsumerJetStream`.
- **Privacy:** texts sealed with the author's key; events, notices, logs and pushes carry IDs and codes only (tests check logs, outbox and tables for fake text). Display names are first names only; never phones.
- **Group rooms:** exactly one room per district and per needs code (partial unique indexes); `birge migrate` creates the missing ones for active reference data every run and never changes or removes existing ones. Titles are locale keys `chat.room.district.<code>` / `chat.room.topic.<code>`, which the clients resolve with the reference labels (`district.<code>`, `needs.<code>`). No member is joined automatically; she joins her own district's room herself.
- **No deletion path in normal operation:** members leave, reports close, retention clears the text and keeps the row.
- **Account deletion** (AGENTS.md non-negotiable 6, ADR-0019): every message she wrote becomes a "message deleted" tombstone like retention does (body cleared, `purged_at` set; row, sequence number and `author_id` stay), so the other members' history and the gap-free sequences stay whole; her name is no longer found in membership and is shown as deleted. Messages under legal hold are left untouched and logged as `DeletionSkippedLegalHold` with the message ID only. Her reports, room memberships, restrictions and mentor assignments are deleted; direct rooms with her stay for the other member; the push timers of the purged messages are cancelled. The crisis-escalation retry timer is left running (crisis path). One transaction; one audit entry `chat.member_deleted` (actor system).

## Failure modes

- Database down: sends, history and moderation fail (500 / WebSocket 1011); the crisis notice is still returned to the author with the error and the escalation is attempted directly (safety needs the same database, so it usually fails too and is logged as `ChatCrisisEscalationFailed`).
- Safety down: the crisis message is stored held, the author gets the phones, `ChatCrisisEscalationDeferred` is logged at Error, and the timer retries until the escalation opens.
- Membership down: the crisis check still runs (it never waits for membership); listing, rendering and pushes fail closed (no message is shown without a block check); a WebSocket whose render fails is closed so the client resumes instead of missing a message.
- NATS down: messages are stored; live delivery stops; clients see new messages on the next notice (gap fill) or reconnect (resume by `afterSeq`).
- JetStream / broker down: direct rooms open when the events arrive (outbox of the origin context).
- A gateway replica dies: its clients reconnect to any replica and resume by sequence.

## Tests

- Unit: `model/model_test.go` (filters, visibility, moderation matrix, validation, push ids, retention schedule); `service/moderation_test.go` (ApplyModerationDecided and ApplyMentorRoleChanged early returns); `api/handler_test.go` (error mapping).
- Integration (`adapter/integration_test.go`, `testinfra.Postgres`, `testinfra.NATS`): idempotent send incl. concurrent replays; 50 concurrent sends; crisis held + escalation + author notice + timer retry while safety is down + replay; link/phone holds and restore; newcomer hold; rate limit and slow mode; blocks both ways in history, rendering, reports and push; push throttle, mute and presence; mentor limits and audit; mentor role revoked through the JetStream consumer (all her rooms, other mentors kept, audit per room, duplicate delivery and replay after reassignment are no-ops); retention with report and legal-hold exemptions; shredded author; room join/list rules; direct rooms exactly once per origin (redelivery and re-publish) and through the JetStream consumer; group rooms ensured once under concurrent ensures and repeated runs, existing ids and titles kept, a deactivated district's room kept (`TestEnsureGroupRoomsPostgres`). `birge migrate` end to end: `internal/wiring/migrate_seed_test.go`.
- Gateway (`gateway/gateway_test.go`): two replicas over real NATS (happy path, crisis frame private, errors, ping), resume by seq and gap fill after a lost notice, leave ends the subscription, 4401 on a bad token, slow client disconnected (1013) while a fast client in the same room receives every message.
- Also: `model/room_test.go`; `service/{background,deletion,messages,moderate,rooms}_test.go` (with `env_test.go` as the shared setup); `api/handler_test.go`, `api/handler_http_test.go`; `gateway/frames_test.go`; `adapter/erase_fanout_test.go`.

## Open questions

- **Muted vs banned:** a `mute_1h` member gets `banned` (no `muted` code in the contract).
- **Partitioning:** ADR-0016 names a time-partitioned message table; this version uses one table with a retention index.
- **Presence** is one row per member and room: closing one of two devices marks her disconnected until the other's next heartbeat (≤ 30 s), so one extra push is possible.
