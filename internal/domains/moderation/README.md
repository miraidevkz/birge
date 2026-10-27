# moderation

The staff moderation queue (`docs/requirements/mvp.md` § 6): listings, specialists, reviews, chat reports, held chat messages and verification requests wait here for a moderator's decision. Moderation owns the queue and the decisions, never the content: the owning context shows the content through a `ContentSource` port when the queue is read, and applies the decision when it consumes `moderation.Decided`. ADR-0016 (chat moderation), ADR-0019 (blocks, legal hold).

## Owns

| Table (schema `moderation`) | Purpose | Personal data |
|---|---|---|
| `items` | One item per object under review: `kind` (`listing`, `specialist`, `review`, `report`, `chat_message`, `verification`), `object_id` (the owner's ID: listing, specialist, review, chat message for `report` and `chat_message`, verification request), `status` `open`/`decided`, `source_reason` (hold or report reason code from the event), `report_count`, `created_at`, and the decision: `decided_by` (staff ID), `decided_at`, `approve`, `reason` (code), `block_author`, `legal_hold` | none: IDs and codes only; no content, no summaries |

Uses `platform.processed_events` (consumer `moderation.Queue`), `platform.outbox` and `platform.audit_log`.

## API

Operations of tag `moderation` in `contracts/openapi/birge.yaml` (`/v1/staff/*`, staff OIDC). Every operation needs a staff principal with MFA and role `moderator` or `admin` (deny by default: members, anonymous → 401; `psychologist`, `lawyer`, no MFA → 403). This covers verification, block author and legal hold too.

| Operation | Rules not in the contract |
|---|---|
| `GET /staff/moderation/queue` | Open items only, oldest first (`created_at`, then `id`), optional `kind`; pages of 50; the cursor is opaque (last `created_at` and `id`). `summary` comes from the kind's `ContentSource`, cut to 2000 characters; `photoUrl` only for listings; `reportReason` / `reportCount` only for reports; `holdReason` only for `chat_message`. Object gone or purged → empty summary; a source outage fails the page (500) rather than show an item without its content; other kinds can still be listed with `kind`. |
| `POST /staff/moderation/items/{id}/decision` | Unknown item → 404; already decided → 409 `already_decided` (a decision is final). Validated against the kind (400): a rejection needs a `reason`, an approval carries none; `blockAuthor` and `legalHold` only for `report` and `chat_message` and only with a rejection (ADR-0019: the content is hidden). For a report, approve = keep the message (report dismissed), reject = take it down. |

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `moderation.Decided` v1 | a decision | outbox, same transaction as the item update and the audit entries |
| consumes | `sharing.ListingSubmitted` v1 | → item `listing` | JetStream durable consumer `moderation-queue`; deduplicated by event id (`platform.processed_events`) and by one open item per `(kind, object_id)` |
| consumes | `directory.ItemSubmitted` v1 | → item `specialist` or `review` | as above; a resubmitted review after a decision opens a new item |
| consumes | `chat.MessageHeld` v1 | → item `chat_message` (hold reason kept) | as above |
| consumes | `chat.MessageReported` v1 | → item `report`; a further report on a message with an open report item increments `report_count` | as above; a redelivered report is not counted twice |
| consumes | `membership.VerificationRequested` v1 | → item `verification`, object = `request_id` | as above |

Accepting an event needs only the moderation database. A malformed payload is terminated, not redelivered (`ModerationEventMalformed`, logged without the payload).

## Timers

None.

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `Service.HandleEvent(ctx, eventID, type, data)` / `Submit(ctx, eventID, submission)` | Idempotent item creation as above; unknown types ignored; `model.ErrInvalidInput` for a malformed payload. Used by `adapter.QueueConsumer`. |

Ports (wired in `internal/wiring`): `ContentSource` per kind — chat (message text for `report` and `chat_message`), sharing (listing title and description, processed photo URL), directory (specialist card without contacts; review text), membership (verification: first name and district only). Implementations return no contact data and `model.ErrNotFound` for a gone object. `Store` is `adapter.PostgresStore`.

## Configuration

None. Fixed: page size 50 (contract maximum 100), summary 2000 characters.

## Invariants

- **One open item per object:** partial unique index `items_open_object_uidx (kind, object_id) WHERE status = 'open'`; inserts use `ON CONFLICT … DO NOTHING` (reports: `DO UPDATE report_count + 1`). `TestItemCreationIsIdempotentPostgres` (incl. 10 concurrent submissions), `TestRepeatedReportsBumpCounterPostgres`.
- **A decision is final and single:** the item row is locked (`FOR UPDATE`) and updated only while `open`; concurrent decisions serialise and exactly one wins. `TestConcurrentDecisionsOneWinsPostgres`.
- **Decision, audit and event are atomic:** one transaction; a failing outbox write rolls back the item and the audit entries. `TestDecisionIsAtomicPostgres`, `TestDecisionWritesEventAndAuditPostgres`.
- **Audited:** `platform.audit_log`, actor = staff ID with role `moderator` or `admin` (admin when both), object `moderation.item`: `moderation.approve`, or `moderation.reject` with the reason code; plus `moderation.block_author` and `moderation.legal_hold` when set.
- **Shape of a decision:** database CHECKs mirror the service — reject needs a reason; block author and legal hold only for `report` and `chat_message`; open items carry no decision fields. Matrix in `model.ValidateDecision`, `model/model_test.go`.
- **No content:** summaries are never stored, logged or put into events; `source_reason` is kept only when it is a short code. `TestNoContentInDatabaseOrLogsPostgres` checks items, outbox, audit and logs for fake content, also when a source fails.
- **No deletion path:** items move from `open` to `decided` and stay.
- **Account deletion needs nothing here**, so this context has no `privacy.DataDeletionRequested` consumer. An item holds only IDs and codes: the object ID (a listing, review, message or verification request, which its owning context deletes or tombstones), staff IDs and decision codes, never a member ID or content. Summaries are fetched from the owner when the queue is read, so an item about deleted content shows nothing personal. The items stay as the record of moderation decisions (legal hold, ADR-0019). The deletion SQL check in `internal/wiring/deletion_e2e_test.go` enforces that no item contains the deleted member's ID.

## Failure modes

- Database down: the queue, decisions and event consumption fail; the consumer naks and JetStream redelivers (up to 10 times; then the owner's outbox has already published, so a lost item needs a re-publish).
- An owning context down: event consumption is unaffected; listing that kind fails (`ModerationSummaryUnavailable`), other kinds can be listed with `kind`; decisions work.
- Broker down: items appear when the owners' outboxes publish; `moderation.Decided` waits in the outbox.

## Tests

- Unit: `model/model_test.go` (decision matrix over every kind, staff roles, source reason codes); `service/service_test.go` (deny by default for both operations, cursor, malformed and unknown events); `api/handler_test.go` (error mapping).
- Integration (`adapter/integration_test.go`, `testinfra.Postgres`): idempotent creation for all five events, redelivery, concurrent submissions, resubmission after a decision; report counter; decision → outbox event + audit rows; double decision 409; 400/403/404 leave the item open; atomic rollback on outbox failure; 12 concurrent decisions → one winner, one event, one audit row; queue order, kind filter, cursor pages, gone content and source outage; no fake content in tables or logs; consumer envelope handling.

## Open questions

- **Cursor across new items:** items are ordered by the time moderation received the event; an event redelivered late lands at its receive time, not its submission time.
- **`membership.MemberVerified.verified_by`:** `moderation.Decided` carries no staff id, so consumers set `verified_by` to the moderation item id.
