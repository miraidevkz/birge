# membership

Members, children, verification requests, blocks (ADR-0019) and reference data (cities, districts, needs). Serves onboarding and profile in `docs/requirements/mvp.ru.md` § 4.1. Sign-in is handled by `authgw/` in front of Kratos (ADR-0008 addendum).

## Owns

| Table (schema `membership`) | Purpose | Personal data |
|---|---|---|
| `cities` | Deployment city reference | none |
| `districts` | Districts of a city | none |
| `needs` | Special-needs codes (labels in locale files) | none |
| `members` | One row per identity; created lazily on first authenticated call | `display_name` encrypted (Encryptor / ADR-0009); `identity_id` is an ID |
| `children` | Child cards (birth year + needs); no names | none |
| `verification_requests` | Moderator verification queue | none |
| `blocks` | Mutual invisibility (ADR-0019) | none |

## API

Operations of tag `membership` in `contracts/openapi/birge.yaml`:

| Operation | Rules not in the contract |
|---|---|
| `GET/PATCH /me` | Member session only (staff refused). First `GET` creates the member and publishes `membership.MemberRegistered` once (unique `identity_id`). Display name sealed before store; never logged. |
| `POST /me/children`, `PUT/DELETE /me/children/{id}` | Own children only. `POST` is idempotent on `Idempotency-Key`. Cap `MaxChildren` (10). |
| `POST /me/verification-requests` | At most one pending request; repeats return the current member without a second event. |
| `GET /me/blocks`, `PUT/DELETE /me/blocks/{memberId}` | Idempotent block/unblock; no self-block; cap `MaxBlocks` (1000). Blocked member is not notified. |
| `GET /reference/districts`, `GET /reference/needs` | Anonymous (`security: []`). |

Auth gateway (`authgw/`, mounted at `/auth/`): PoW (`X-Pow-Solution`), E.164 prefix allow-list, per-IP / per-number / global rate limits before proxying to Kratos. Limits are process-local (one replica).

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `membership.MemberRegistered` v1 | first authenticated request of a new identity | outbox, same transaction as member insert |
| publishes | `membership.VerificationRequested` v1 | member asks for a call and none is pending | outbox, same transaction as pending request |
| consumes | none | | |

`membership.MemberVerified` exists as a contract for the moderator side; this context does not publish it yet.

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| none | | | |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `service.Service.IsBlocked(ctx, a, b)` | Symmetric block check (either direction). No authorization. |
| `service.Service.Member(ctx, id)` | Member by ID or `model.ErrMemberNotFound`. No authorization; display name is opened for the caller. |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `authgw.Config.KratosPublicURL` | required | Cluster-internal Kratos public API base URL |
| `authgw.Config.PoWKey` / `HashKey` | required (≥32 bytes, distinct) | PoW HMAC and number/IP hash keys from the secret store |
| `authgw.Config.PoWMaxNumber` | required (>0) | PoW work factor |
| `authgw.Config.AllowedPrefixes` | required | Allowed E.164 mobile prefixes |
| `authgw.Config.PerIP` / `PerNumber` / `Global` | required | Token-bucket rates |
| `authgw.Config.PhoneTrait` | `phone` | Registration trait holding the phone |
| TODO(verify): env names for wiring | | `cmd/birge` wiring of authgw and Encryptor is not in this tree yet |

## Invariants

- Lazy member creation; unique `identity_id` (database + service); concurrent first calls yield one member and one `MemberRegistered` (service + unique constraint).
- A member only reads/changes her own data; principal from `internal/platform/auth`; staff sessions refused on member routes (service + API).
- Display name sealed via `port.Encryptor` (ADR-0009); never logged; events carry IDs only (service + `Member.LogValue`).
- Children carry no names (schema + model).
- At most one pending verification request per member (partial unique index + service).
- Blocks: no self-block, idempotent, mutual invisibility via `IsBlocked` (schema CHECK + service; ADR-0019).

## Failure modes

- Database down: HTTP handlers and other contexts calling `IsBlocked`/`Member` fail; no partial member create without outbox (same transaction).
- Outbox/broker lag: events stay in the outbox until the relay publishes (ADR-0007); domain rows are already committed.
- Kratos unreachable: auth gateway returns upstream/timeout errors; session middleware returns 503 (platform `session`).
- Encryptor unavailable: profile updates that set a name fail; reads that need Open fail.

## Tests

- Unit: `service/service_test.go`, `model/model_test.go`, `authgw/authgw_test.go`, `api/server_test.go` — lazy create, idempotent children/verification/blocks, authgw PoW and rate limits, HTTP mapping.
- Integration: `adapter/postgres_integration_test.go` — PostgreSQL store + outbox with `testinfra.Postgres`.
