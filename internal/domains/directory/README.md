# directory

Specialists and reviews («Специалисты»): search by profession, district and need; cards show the average rating and the three latest reviews (first-name-only authors). Verified members suggest specialists and put reviews; both go through moderation. Serves `docs/requirements/mvp.md` § 4.5.

## Owns

| Table (schema `directory`) | Purpose | Personal data |
|---|---|---|
| `specialists` | Name, profession, district, needs[], optional public work contacts, status `pending\|active\|rejected`, `added_by` | Specialist/organisation name and public contacts (not a mother's personal data) |
| `reviews` | One per specialist+author: rating, text, status; resubmit replaces and returns to pending | Review text; author is an id (display name from membership at read time) |

## API

Operations of tag `directory` in `contracts/openapi/birge.yaml`. All are member-only: the caller is `auth.Principal.MemberID`; staff, anonymous and sessions without a member id → 401.

| Operation | Rules not in the contract |
|---|---|
| `GET /specialists` | Active only; filters by profession/district/need; average rating over active reviews; 3 latest active reviews with first-name-only author names (`MemberInfo`); 20 per page, keyset cursor. |
| `POST /specialists` | Verified only (403). Idempotent per `Idempotency-Key`. Status `pending`; emits `directory.ItemSubmitted` (`object_type=specialist`). |
| `PUT /specialists/{id}/reviews` | Verified only. One review per member per specialist; resubmitting replaces and goes back to pending; emits `directory.ItemSubmitted` (`object_type=review`). |

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `directory.ItemSubmitted` v1 | specialist or review submitted | outbox, same transaction; `object_type`, `object_id` only |
| consumes | `moderation.Decided` v1 | kinds `specialist` and `review` | deduplicated via `platform.processed_events` (consumer `directory.moderation.Decided`) |
| consumes | `privacy.DataDeletionRequested` v1 | account deletion | JetStream durable `directory-deletion` (`wiring.DeletionConsumer`); deduplicated in `platform.processed_events` (consumer `directory.Deletion`) after `service.Deletion.DeleteMember`, which is idempotent and runs in one transaction |

## Timers

None.

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `Service.ApplyModerationDecided` | Applies `moderation.Decided` for specialist and review. |
| `Service.SpecialistSummary` / `ReviewSummary` | ContentSource text without contacts. |

Ports used by this context (wired later): `MemberInfo` (verification + first name), `Store` (PostgreSQL adapter).

## Configuration

None beyond platform defaults.

## Invariants

- Verified members only suggest specialists and put reviews (service; 403).
- One review per (specialist, author) (unique index + upsert); resubmit keeps the row id and returns to pending.
- Search shows only active specialists; ratings and latest reviews use only active reviews (queries + service).
- Review author names are first name only (service `firstName` on `MemberInfo.DisplayName`).
- No names or review text in events or logs (service; tests).
- Nothing is deleted in normal operation: status changes only. The only delete queries belong to account deletion.
- Account deletion (AGENTS.md N6): her reviews are deleted. They are her own text and no other member's record hangs on them; a tombstone would need a nullable `content`, and it would keep a rating nobody can stand behind. Specialist cards she suggested describe the specialist, not her: active cards and cards other members reviewed stay as tombstones (`added_by` kept, and her name no longer resolves); her pending or rejected suggestions that nobody reviewed are deleted. A moderation item about a deleted object keeps only IDs and codes, and a later `moderation.Decided` for it is a no-op (not found). One transaction; one audit entry `directory.member_deleted` (actor system).

## Failure modes

- Database down: every operation fails with 500; nothing is half-written.
- Broker down: events wait in the outbox; relay publishes later (ADR-0007).
- Membership down (`MemberInfo`): suggest, put-review and search (for author names) fail.

## Tests

- Unit: `service/service_test.go` — suggest rules, one review per member, ratings, first-name-only authors, moderation dedup; logs and events checked for fake content.
- Integration: `adapter/integration_test.go` with `testinfra.Postgres` — upsert review, ratings, search.
- Fakes: `directorytest/`.
