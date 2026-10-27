# privacy

Versioned consents and account deletion (crypto-shredding after 24 hours).

## Owns

- Schema `privacy`: `consent_history` (append-only), `deletion_requests`
- HTTP: `GET|POST /me/consents`, `POST /me/deletion-request`
- Events: `privacy.ConsentChanged`, `privacy.DataDeletionRequested`
- Timer kind (ADR-0020): `privacy.DeletionDue` — due at `requested_at + 24h`
  (config); dedup key = member id

## Invariants

- Consent history is never updated or deleted; current state is the latest row
  per `(member_id, type)`.
- `granted` in API responses and `HasConsent` are true only when the latest
  row is a grant for the configured current text version.
- `setConsent` rejects unknown types and versions other than the current one
  (400); the change and `privacy.ConsentChanged` are written in one transaction.
- One deletion request per member: repeated `requestDeletion` creates one row,
  one event and one timer.
- The deletion timer marks the request completed and calls `KeyShredder.Shred`
  once per successful run (ADR-0009); a completed request skips shred on retry.
- Logs and events carry IDs and event names only — never consent text.

## Ports

| Port | Purpose |
|---|---|
| `KeyShredder` | Destroy the member DEK (fake in tests; platform crypto later) |

Exported for other contexts: `Service.HasConsent(ctx, memberID, type)`.

Wiring in `cmd/birge` is out of scope for wave 1.
