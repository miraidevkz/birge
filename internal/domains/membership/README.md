# membership

Members, children, verification requests, blocks (ADR-0019), staff suspension and reference data (cities, districts, needs). Serves onboarding and profile in `docs/requirements/mvp.md` § 4.1. Sign-in is handled by `authgw/` in front of Kratos (ADR-0008 addendum).

## Owns

| Table (schema `membership`) | Purpose | Personal data |
|---|---|---|
| `cities` | Deployment city reference | none |
| `districts` | Districts of a city | none |
| `needs` | Special-needs codes (labels in locale files) | none |
| `members` | One row per identity; created lazily on first authenticated call; optional `verified_at`; optional `suspended_at` / `suspended_reason` from moderation `block_author`; after account deletion a tombstone (`deleted_at`) | `display_name` encrypted (Encryptor / ADR-0009); `identity_id` is an ID |
| `children` | Child cards (birth year + needs); no names | none |
| `verification_requests` | Moderator verification queue (`reject_reason` when rejected) | none |
| `blocks` | Mutual invisibility (ADR-0019) | none |

## API

Operations of tag `membership` in `contracts/openapi/birge.yaml`:

| Operation | Rules not in the contract |
|---|---|
| `GET/PATCH /me` | Member session only (staff refused). First `GET` creates the member and publishes `membership.MemberRegistered` once (unique `identity_id`). Display name sealed before store; never logged. Suspended members → 403 `member_suspended`. |
| `POST /me/children`, `PUT/DELETE /me/children/{id}` | Own children only. `POST` is idempotent on `Idempotency-Key`. Cap `MaxChildren` (10). |
| `POST /me/verification-requests` | At most one pending request; repeats return the current member without a second event. |
| `GET /me/blocks`, `PUT/DELETE /me/blocks/{memberId}` | Idempotent block/unblock; no self-block; cap `MaxBlocks` (1000). Blocked member is not notified. |
| `GET /reference/districts`, `GET /reference/needs` | Anonymous (`security: []`). |
| `GET /staff/members` | Staff `moderator` or `admin`, MFA. Lists verified or suspended members (`status=verified|suspended`, optional `districtId`, keyset `cursor`). Page size 50. Every read is audited as `membership.member.list` (staff ID, status filter, count; never names). Tombstoned members never appear. |
| `GET /staff/members/{memberId}/suspension` | Staff `moderator` or `admin`, MFA. |
| `DELETE /staff/members/{memberId}/suspension` | Staff `admin`, MFA; lifts suspension; audited. |

Auth gateway (`authgw/`, mounted at `/auth/`): PoW (`X-Pow-Solution`), E.164 prefix allow-list, per-IP / per-number / global rate limits before proxying to Kratos. Limits are process-local (one replica).

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `membership.MemberRegistered` v1 | first authenticated request of a new identity | outbox, same transaction as member insert |
| publishes | `membership.VerificationRequested` v1 | member asks for a call and none is pending | outbox, same transaction as pending request |
| publishes | `membership.MemberVerified` v1 | verification approved via `moderation.Decided` | outbox, same transaction as approve; `verified_by` = moderation item id (Decided carries no staff id) |
| consumes | `moderation.Decided` v1 | kind `verification` | JetStream durable `membership-moderation-decided`; deduplicated via `platform.processed_events`; a deleted member (request gone) is a no-op |
| consumes | `privacy.DataDeletionRequested` v1 | account deletion | JetStream durable `membership-deletion` (`wiring.DeletionConsumer`); deduplicated in `platform.processed_events` (consumer `membership.Deletion`) after `service.Deletion.DeleteMember`, which is idempotent and runs in one transaction |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `membership.DeleteIdentity` | account deletion turns the member into a tombstone (subject = Kratos identity ID, due at once) | `service.Deletion.DeleteIdentity`: `DELETE /admin/identities/{id}` on the Kratos admin API | 204 and 404 are done; any other answer fails and the timer retries with backoff; scheduled only by the run that creates the tombstone |

## Service API for other contexts

| Function | Callers may rely on |
|---|---|
| `service.Service.IsBlocked(ctx, a, b)` | Symmetric block check (either direction) **or** either member suspended. A deleted member is not "blocked" (her tombstones stay visible). No authorization. |
| `service.Service.Member(ctx, id)` | Member by ID or `model.ErrMemberNotFound` (also for a deleted member: callers show her as deleted). No authorization; display name is opened for the caller. |
| `service.Service.ListVerifiedInDistrict(ctx, districtID)` | IDs of verified, non-suspended, non-deleted members in the district. No authorization. |
| `service.Service.SuspendAuthor(ctx, memberID, reason, itemID)` | System suspension from chat after `block_author`; idempotent; audited as `system`. A deleted member is a no-op. |
| `service.Service.VerificationSummary(ctx, requestID)` | First name and district id for the moderation ContentSource; `ErrMemberNotFound` when gone. |
| `service.Service.ApplyModerationDecided` | Applies `moderation.Decided` for kind verification. |
| `service.Deletion.DeleteMember(ctx, memberID)` | Account deletion (system only, no principal). Idempotent; one transaction. |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `authgw.Config.KratosPublicURL` | required | Cluster-internal Kratos public API base URL |
| `authgw.Config.PoWKey` / `HashKey` | required (≥32 bytes, distinct) | PoW HMAC and number/IP hash keys from the secret store |
| `authgw.Config.PoWMaxNumber` | required (>0) | PoW work factor |
| `authgw.Config.AllowedPrefixes` | required | Allowed E.164 mobile prefixes |
| `authgw.Config.PerIP` / `PerNumber` / `Global` | required | Token-bucket rates |
| `authgw.Config.PhoneTrait` | `phone` | Registration trait holding the phone |
| `adapter.Postgres.SeedReferenceIfEmpty` | — | `birge migrate` seeds DEMO cities/districts/needs from `presets/demo/reference.json` only when those tables are empty |
| see `docs/development.md` § Configuration | | all variables, defaults and which subcommand reads them |

## Invariants

- The auth gateway never forwards `Cookie`, `Origin` or its PoW header to Kratos: members (native and web) use the API flow with a session token, and Kratos refuses an API flow that carries `Origin` (authgw).
- Lazy member creation; unique `identity_id` (database + service); concurrent first calls yield one member and one `MemberRegistered` (service + unique constraint).
- A member only reads/changes her own data; principal from `internal/platform/auth`; staff sessions refused on member routes (service + API).
- Display name sealed via `port.Encryptor` (ADR-0009); never logged; events carry IDs only (service + `Member.LogValue`).
- Children carry no names (schema + model).
- At most one pending verification request per member (partial unique index + service).
- Blocks: no self-block, idempotent, mutual invisibility via `IsBlocked` (schema CHECK + service; ADR-0019).
- Suspension: `suspended_at` and `suspended_reason` both set or both null; suspended members get 403 `member_suspended` on member routes and are hidden like a block.
- Staff member list (`/staff/members`): only `displayName` and `districtId` from the member, plus `verified`/`verifiedAt`, `suspended`/`suspendedAt` (never phone, children, needs, language). Keyset paging (most recent `verified_at` / `suspended_at` first), page size 50. Every read is audited in `platform.audit_log`; deleted members never appear.
- Account deletion (AGENTS.md N6, ADR-0009): children, verification requests and blocks in both directions are deleted; the member row becomes a tombstone that keeps only `id` and `identity_id` (`display_name`, district, roles and suspension cleared, `deleted_at` set) and is never deleted. A deleted account has no crisis exemption: `CrisisMemberID` refuses it with 401 like every other route. `moderation.Decided` for her (verification, `SuspendAuthor`) is a no-op. Why: the identity must stay refused. Every session of it gets 401 (`model.ErrAccountDeleted` in `ensure` and the session middleware) and lazy creation never makes a new member for it, also after the Kratos identity is gone and an old token is replayed. `Member(id)` does not find a tombstone. One audit entry `membership.member_deleted` (actor system). A moderation item about a deleted verification request keeps only its IDs.

## Failure modes

- Database down: HTTP handlers and other contexts calling `IsBlocked`/`Member` fail; no partial member create without outbox (same transaction).
- Outbox/broker lag: events stay in the outbox until the relay publishes (ADR-0007); domain rows are already committed.
- Kratos unreachable: auth gateway returns upstream/timeout errors; session middleware returns 503 (platform `session`).
- Encryptor unavailable: profile updates that set a name fail; reads that need Open fail.
- Kratos admin API down during deletion: the member data is already erased; `membership.DeleteIdentity` retries until Kratos answers, and the tombstone refuses the identity meanwhile.

## Tests

- Unit: `service/service_test.go`, `service/deletion_test.go` (tombstone refuses the identity, retries), `model/model_test.go`, `authgw/authgw_test.go`, `api/server_test.go` — lazy create, idempotent children/verification/blocks, authgw PoW and rate limits, HTTP mapping.
- Integration: `adapter/postgres_integration_test.go` — PostgreSQL store + outbox with `testinfra.Postgres`.
- Wiring: `internal/wiring/moderation_e2e_test.go` — verification approve and suspension after `block_author`.

Suspension (moderation `block_author`) refuses every member route with 403 `member_suspended`, except the crisis and own-data routes (`crisisExempt` in wiring): help request, raising and closing her SOS, the emergency plan, consents and the deletion request (non-negotiable 1). `CrisisMemberID` resolves her for those.
