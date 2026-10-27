# sharing

Item listings («Поделиться»): give / swap / need with optional item photos, a district feed, and contact requests that open a private chat only with the author's consent. Serves `docs/requirements/mvp.md` § 4.5. Photos are item-only (ADR-0019); the server re-encodes them and strips metadata (ADR-0015).

## Owns

| Table (schema `sharing`) | Purpose | Personal data |
|---|---|---|
| `listings` | One listing: type, category, title, description, district, optional photo upload / processed object key, status `pending\|active\|rejected\|closed`, reject reason, `approved_at`, `closes_at` | Title and description (not sealed; shown in the feed); no contacts |
| `photo_uploads` | Presigned upload slot: object key, content type, size, expiry, status `pending\|processed\|failed`, processed key, `used_at` | none (bytes live in object storage) |
| `contact_requests` | One request per listing+requester; status `pending\|accepted` | IDs only |

## API

Operations of tag `sharing` in `contracts/openapi/birge.yaml`. All are member-only: the caller is `auth.Principal.MemberID`; staff, anonymous and sessions without a member id → 401.

| Operation | Rules not in the contract |
|---|---|
| `POST /listings/photo-uploads` | Verified only (403). JPEG/PNG ≤ 5 MB. Returns a 10-minute presigned PUT URL (`ObjectStore`). When object storage is unset: 503 `storage_not_configured`. |
| `POST /listings` | Verified only; `acceptRules` must be true. Idempotent per `Idempotency-Key`. Status `pending`; emits `sharing.ListingSubmitted`. With a photo, schedules `sharing.ProcessPhoto`; stays pending until the photo is processed and moderation approves. |
| `GET /listings` | Active only, newest first, 5 per page, keyset cursor; filters by type/category/district; never the viewer's own; blocked members excluded both ways (ADR-0019). |
| `GET /me/listings` | Caller's listings in every status (≤ 100). |
| `POST /listings/{id}/close` | Author only; active → closed; cancels auto-close timer; idempotent. Another member's id → 404. |
| `POST /listings/{id}/contact-requests` | Active listing only; not own; not blocked; idempotent per listing+requester; notifies the author (`sharing.contact_request`, params `{contact_request_id}`). |
| `POST /me/listing-contact-requests/{id}/accept` | Listing author only; emits `sharing.ContactAccepted` once (chat opens the room). |

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `sharing.ListingSubmitted` v1 | listing created (not on replay) | outbox, same transaction; `listing_id` only |
| publishes | `sharing.ContactAccepted` v1 | author accepts | outbox, same transaction; listing/author/requester ids |
| publishes | `notify.DeliveryRequested` v1 | contact request; listing decided (`sharing.listing_decided`); class `normal` | outbox, same transaction; deterministic `request_id` |
| consumes | `moderation.Decided` v1 | kind `listing` | deduplicated via `platform.processed_events` (consumer `sharing.moderation.Decided`) |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `sharing.ProcessPhoto` | listing created with a photo; due = now; subject = listing id | decode upload, re-encode JPEG ≤ 1600 px, strip metadata, store processed key, delete original; try activate if approved | skipped when already processed/failed; permanent decode failure marks failed and returns nil |
| `sharing.AutoClose` | listing activated; due = `closes_at` (`ListingTTL`, 30d); subject = listing id | close if still active | cancelled when the author closes |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `Service.ApplyModerationDecided` | Applies `moderation.Decided` for kind listing. |
| `Service.ModerationSummary` | Title, description and photo URL for the moderation ContentSource. |
| `Service.HandleProcessPhoto` / `HandleAutoClose` | Timer handlers; used by `adapter.TimerHandlers`. |

Ports used by this context: `MemberInfo`, `Blocks`, `ObjectStore` (`internal/platform/objectstore`), `ImageProcessor` (`imageproc`), `Store` (PostgreSQL adapter).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Settings.ListingTTL` | 30d | Active listing lifetime before auto-close |

## Invariants

- Verified members only create listings and photo uploads (service; 403).
- `acceptRules` must be true (service; 400).
- Feed never shows the viewer's own listing or blocked authors either way (query + service; fail closed).
- Listings show only processed photos; unprocessed or failed photo keeps the listing pending even after approval (service + schema).
- Contact requests are unique per listing+requester (unique index + service).
- No titles, descriptions or photo bytes in events, notifications or logs (service; tests).
- Nothing is deleted: listings close, contact requests change status (no delete queries). Photo original objects are deleted from object storage after processing (ADR-0015).

## Failure modes

- Database down: every operation fails with 500; nothing is half-written; timer worker retries with backoff (ADR-0020).
- Broker down: events wait in the outbox; relay publishes later (ADR-0007).
- Object store down: photo upload / process / feed photo URLs fail; listing create without photo still works.
- Membership down (`MemberInfo`/`Blocks`): create and feed fail (fail closed).

## Tests

- Unit: `imageproc/imageproc_test.go` — EXIF stripped, garbage rejected; `service/service_test.go` — create rules, feed blocks/own, contact flow, moderation dedup, photo gate + EXIF strip, close/auto-close; logs and events checked for fake content.
- Integration: `adapter/integration_test.go` with `testinfra.Postgres` — photo process, feed blocks, contact accept, outbox `ContactAccepted`.
- Fakes: `sharingtest/`.
