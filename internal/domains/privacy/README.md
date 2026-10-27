# privacy

Versioned consents and account deletion via crypto-shredding (ADR-0009). Serves consents and Delete my data («Удалить мои данные») in `docs/requirements/mvp.md` § 4.1 and § privacy / deletion (≈ § 8 / acceptance).

## Owns

| Table (schema `privacy`) | Purpose | Personal data |
|---|---|---|
| `consent_history` | Append-only grant/revoke history per type and text version | none (type/version/granted only; no consent text) |
| `deletion_requests` | One deletion request per member; `completed_at` after shred | none |

## API

Operations of tag `privacy` in `contracts/openapi/birge.yaml`:

| Operation | Rules not in the contract |
|---|---|
| `GET /me/consents` | Member session only (staff refused). Returns every known type; `granted` is true only when the latest row is a grant for the configured current text version. |
| `POST /me/consents` | Rejects unknown types and versions other than the current one (400). Appends a history row and publishes `privacy.ConsentChanged` in one transaction. |
| `POST /me/deletion-request` | Body must be `{ "confirm": "DELETE" }`. Idempotent per member: one row, one event, one timer. Due at `requested_at + DeletionDueAfter` (default 24h). |

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `privacy.ConsentChanged` v1 | consent granted or revoked | outbox, same transaction as history append |
| publishes | `privacy.DataDeletionRequested` v1 | first successful deletion request | outbox, same transaction as insert + timer schedule |
| consumes | none | | |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `privacy.DeletionDue` | on first deletion request (dedup key = member id) | `HandleDeletionDue`: `KeyShredder.Shred`, then set `completed_at` | completed request skips shred; mark-completed is once |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `service.Service.HasConsent(ctx, memberID, type)` | True only when the latest row is a grant for the configured current text version. No authorization. |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Settings.CurrentVersions` | required map | Consent type → current text version string |
| `service.Settings.DeletionDueAfter` | 24h | Delay from request to shred |
| see `docs/development.md` § Configuration | | all variables, defaults and which subcommand reads them |

## Invariants

- Consent history is never updated or deleted; current state = latest row per `(member_id, type)` (schema + service).
- `granted` / `HasConsent` require latest row grant **and** version == configured current (service; tested).
- `SetConsent` only accepts the current version (service).
- One deletion request per member (PK + service idempotency).
- Deletion timer shreds then marks completed; retry after completion does not shred again (service; ADR-0009).
- Logs and events: IDs and event names only — never consent text (service).

## Failure modes

- Database down: consent/deletion HTTP fails; timer handler fails and the platform timer retries with backoff (ADR-0020).
- Outbox/broker down: consent/deletion rows and timers commit with the outbox; relay catches up later (ADR-0007).
- `KeyShredder` fails: `completed_at` stays null; timer retry shreds again. Shred runs before mark-completed so a completed row never means “key still live”.
- Shredder not configured: handler returns an error; timer retries.

## Tests

- Unit: `service/service_test.go`, `api/handler_test.go` — consent versioning, idempotent deletion, shred-once, HTTP mapping; fakes in `privacytest/`.
- Integration: `adapter/integration_test.go` — PostgreSQL store, outbox and deletion timer with `testinfra.Postgres`.
