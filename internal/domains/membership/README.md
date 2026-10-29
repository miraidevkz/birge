# membership

Members, children, verification requests, blocks (ADR-0019), staff suspension and reference data (cities, districts, needs). Serves onboarding and profile in `docs/requirements/mvp.md` § 4.1. Sign-in is handled by `authgw/` in front of Kratos (ADR-0008 addendum).

## Owns

| Table (schema `membership`) | Purpose | Personal data |
|---|---|---|
| `cities` | Deployment city reference; `active` (reference sync) | none |
| `districts` | Districts of a city; `active` | none |
| `needs` | Special-needs codes (labels in locale files); `active` | none |
| `members` | One row per identity; created lazily on first authenticated call; optional `verified_at`; optional `suspended_at` / `suspended_reason` from moderation `block_author`; `last_active_day` (activity metric, cleared on deletion); after account deletion a tombstone (`deleted_at`) | `display_name` encrypted (Encryptor / ADR-0009); `identity_id` is an ID |
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
| `GET /reference/districts`, `GET /reference/needs` | Anonymous (`security: []`). Active entries only (a district also needs an active city). |
| `GET /staff/members` | Staff `moderator` or `admin`, MFA. Lists verified or suspended members (`status=verified|suspended`, optional `districtId`, keyset `cursor`). Page size 50. Returns `mentor` boolean flag indicating whether she holds the mentor role. Every read is audited as `membership.member.list` (staff ID, status filter, count; never names). Tombstoned members never appear. |
| `PUT /staff/members/{memberId}/roles/mentor` | Staff `moderator` or `admin`, MFA. Member must exist (404 `not_found`), be verified and not suspended (409). Adds `mentor` to `roles` idempotently. Audited as `membership.member.mentor_grant`. Publishes `membership.MentorRoleChanged` (`mentor: true`) only when the role actually changed. |
| `DELETE /staff/members/{memberId}/roles/mentor` | Staff `moderator` or `admin`, MFA. Removes `mentor` from `roles` idempotently (unknown member → 204). Audited as `membership.member.mentor_revoke`. Publishes `membership.MentorRoleChanged` (`mentor: false`) only when the role actually changed. |
| `GET /staff/members/{memberId}/suspension` | Staff `moderator` or `admin`, MFA. |
| `DELETE /staff/members/{memberId}/suspension` | Staff `admin`, MFA; lifts suspension; audited. |

Auth gateway (`authgw/`, mounted at `/auth/`): PoW (`X-Pow-Solution`), E.164 prefix allow-list, per-IP / per-number / global rate limits before proxying to Kratos. Limits and PoW replay claims live in the `platform/ratelimit` store, shared by every API replica with `BIRGE_RATE_LIMIT_STORE=postgres` (a solution is accepted once across replicas); a store error refuses the request with 503 `auth_unavailable` (fails closed).

Kratos courier hook (`authgw.CourierHook`, not in the public contract): `POST /internal/kratos/courier` on the worker's internal listener (`BIRGE_INTERNAL_ADDR`), never routed by the ingress. `Authorization: Bearer <secret>` from `BIRGE_COURIER_SECRET_FILE`, compared in constant time (401 otherwise). Body `{"to", "template", "code"}` built by `deploy/compose/kratos/courier-hook.jsonnet`; only templates `login_code_valid` and `registration_code_valid` (422 otherwise), E.164 in an allowed range (422), code of 4 to 10 digits (400). The code goes to the interface `authgw.CodeSender`, implemented in wiring by notify's synchronous `SendSignInCode`: 204 sent, 429 refused by the SMS guard, 422 refused for good by the provider, 503 provider failure. Kratos retries every non-2xx.

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `membership.MemberRegistered` v1 | first authenticated request of a new identity | outbox, same transaction as member insert |
| publishes | `membership.VerificationRequested` v1 | member asks for a call and none is pending | outbox, same transaction as pending request |
| publishes | `membership.MemberVerified` v1 | verification approved via `moderation.Decided` | outbox, same transaction as approve; `verified_by` = moderation item id (Decided carries no staff id) |
| publishes | `membership.MentorRoleChanged` v1 | staff granted or revoked the mentor role | outbox, same transaction as role update; published only when the role actually changed |
| publishes | `membership.MemberActiveDay` v1 | first authenticated member request of a calendar day (`BIRGE_TIME_ZONE`) that passes the member middleware (`memberPrincipal` in wiring) | outbox, same transaction as the conditional update of `last_active_day`; at most once per member and day across requests and replicas; best effort: a failure is logged (event name, member ID) and never fails the request. Metrics only (ADR-0024) |
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
| `service.Service.GrantMentorRole(ctx, memberID)` | Staff `moderator` or `admin`, MFA. Verified, non-suspended member only (409 otherwise; 404 for unknown). Audited; outbox event on change. |
| `service.Service.RevokeMentorRole(ctx, memberID)` | Staff `moderator` or `admin`, MFA. Idempotent (unknown member → 204). Audited; outbox event on change. |
| `service.Service.SuspendAuthor(ctx, memberID, reason, itemID)` | System suspension from chat after `block_author`; idempotent; audited as `system`. A deleted member is a no-op. |
| `service.Service.VerificationSummary(ctx, requestID)` | First name and district id for the moderation ContentSource; `ErrMemberNotFound` when gone. |
| `service.Service.ApplyModerationDecided` | Applies `moderation.Decided` for kind verification. |
| `service.Deletion.DeleteMember(ctx, memberID)` | Account deletion (system only, no principal). Idempotent; one transaction. |
| `service.ActiveDays.Record(ctx, memberID)` | Called by the member middleware for every resolved member request. Returns nothing and never fails; after the first request of the day the answer comes from a per-process in-memory cache. No authorization (the middleware already resolved the member). |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `authgw.Config.KratosPublicURL` | required | Cluster-internal Kratos public API base URL |
| `authgw.Config.PoWKey` / `HashKey` | required (≥32 bytes, distinct) | PoW HMAC and number/IP hash keys from the secret store |
| `authgw.Config.PoWMaxNumber` | required (>0) | PoW work factor |
| `authgw.Config.AllowedPrefixes` | required | Allowed E.164 mobile prefixes |
| `authgw.Config.PerIP` / `PerNumber` / `Global` | required | Token-bucket rates |
| `authgw.Config.PhoneTrait` | `phone` | Registration trait holding the phone |
| `authgw.CourierConfig.Secret` | required (≥32 bytes) | Courier hook secret (`BIRGE_COURIER_SECRET_FILE`) |
| `authgw.CourierConfig.AllowedPrefixes` | required | Same list as the gateway (`BIRGE_SMS_ALLOWED_PREFIXES`) |
| `BIRGE_INTERNAL_ADDR` | unset (no hook) | Worker's internal listener for the courier hook |
| `BIRGE_TIME_ZONE` (`service.NewActiveDays` location) | `Asia/Almaty` | Zone whose calendar day defines `membership.MemberActiveDay` |
| `BIRGE_REFERENCE_PRESETS` (migrate; `adapter.Postgres.SyncReference`) | `demo,kz` | Reference sets (`presets/<name>/reference.json`) `birge migrate` syncs into cities, districts and needs: see Reference data below |
| see `docs/development.md` § Configuration | | all variables, defaults and which subcommand reads them |

## Reference data

Cities, districts and needs come from versioned files in `presets/`: `demo` (the synthetic DEMO city, kept for the demo stand) and `kz` (Kazakhstan as of 2026: the 3 cities of republican significance with their districts, the 17 regional centres with one whole-city entry each, the needs list). Each entry has a code and names in kk, ru and en; the database stores codes only and clients label them from the `reference` locale namespace. Every `birge migrate` runs `adapter.Postgres.SyncReference` with the union of the sets in `BIRGE_REFERENCE_PRESETS`, in one transaction under an advisory lock:

- a missing entry is inserted with a stable ID (`model.CityID`, `model.DistrictID`: UUIDv5 from `model.ReferenceNamespace` and the codes), so every deployment and every reseed gives a code the same ID;
- an existing entry is matched by code (districts by city and code) and keeps its ID, whatever it is: the demo stand's rows, created by the earlier seed with database IDs, stay the rows its members, rooms, cards and listings refer to;
- an entry no longer in the sets is deactivated (`active = false`), never deleted; a returning entry is reactivated with its old ID;
- a repeat with the same files changes nothing.

The files own the reference tables: a row added by hand that is in no selected set is deactivated by the next migrate. District codes carry their city prefix and are unique across all sets, because clients label districts and district rooms by code alone (`district.<code>`, `chat.room.district.<code>`).

A deactivated entry is hidden from `GET /reference/*` and refused as a new choice (`unknown_district`, `unknown_need`). A member keeps her deactivated district (a profile update that resends it is not a new choice) and a child card keeps its deactivated needs on edit. Side effects: the sync itself creates no chat rooms; `birge migrate` runs a wiring step right after it that passes the active districts and needs codes to chat (`EnsureGroupRooms`), so every active district has its room on fresh and existing databases; deactivation leaves rooms as they are.

## Invariants

- The auth gateway never forwards `Cookie`, `Origin` or its PoW header to Kratos: members (native and web) use the API flow with a session token, and Kratos refuses an API flow that carries `Origin` (authgw).
- The courier hook never stores, publishes or logs the sign-in code, the number or the secret: it calls a synchronous sender (no event, no outbox, no notification row) and logs only the outcome and template. Only the code is taken from Kratos; the SMS text is fixed in notify, so identity traits and the rendered body never reach the provider (authgw + notify `SendSignInCode`; tests grep the logs).
- Lazy member creation; unique `identity_id` (database + service); concurrent first calls yield one member and one `MemberRegistered` (service + unique constraint).
- A member only reads/changes her own data; principal from `internal/platform/auth`; staff sessions refused on member routes (service + API).
- Display name sealed via `port.Encryptor` (ADR-0009); never logged; events carry IDs only (service + `Member.LogValue`).
- Children carry no names (schema + model).
- `membership.MemberActiveDay` at most once per member and day: `last_active_day` only moves forward (`... AND (last_active_day IS NULL OR last_active_day < day)`), concurrent transactions serialize on the row and only the one that updated the row writes the outbox event. The in-memory cache only saves database work; it holds today's members only and is capped (`activeDayCacheMax`), so clearing it can cost an extra no-op update, never a second event. "Later than" instead of "different from" keeps a replica whose clock is still before midnight from emitting yesterday again (service + query).
- At most one pending verification request per member (partial unique index + service).
- Blocks: no self-block, idempotent, mutual invisibility via `IsBlocked` (schema CHECK + service; ADR-0019).
- Suspension: `suspended_at` and `suspended_reason` both set or both null; suspended members get 403 `member_suspended` on member routes and are hidden like a block.
- Staff member list (`/staff/members`): only `displayName` and `districtId` from the member, plus `verified`/`verifiedAt`, `suspended`/`suspendedAt` (never phone, children, needs, language). Keyset paging (most recent `verified_at` / `suspended_at` first), page size 50. Every read is audited in `platform.audit_log`; deleted members never appear.
- Account deletion (AGENTS.md non-negotiable 6, ADR-0009): children, verification requests and blocks in both directions are deleted; the member row becomes a tombstone that keeps `id`, `identity_id`, `lang` and `created_at` (`display_name`, district, roles, verification, suspension and last active day cleared, `deleted_at` set) and is never deleted. A deleted account has no crisis exemption: `CrisisMemberID` refuses it with 401 like every other route. `moderation.Decided` for her (verification, `SuspendAuthor`) is a no-op. Why: the identity must stay refused. Every session of it gets 401 (`model.ErrAccountDeleted` in `ensure` and the session middleware) and lazy creation never makes a new member for it, also after the Kratos identity is gone and an old token is replayed. `Member(id)` does not find a tombstone. One audit entry `membership.member_deleted` (actor system). A moderation item about a deleted verification request keeps only its IDs.

## Failure modes

- Database down: HTTP handlers and other contexts calling `IsBlocked`/`Member` fail; no partial member create without outbox (same transaction).
- Outbox/broker lag: events stay in the outbox until the relay publishes (ADR-0007); domain rows are already committed.
- Kratos unreachable: auth gateway returns upstream/timeout errors; session middleware returns 503 (platform `session`).
- SMS guard refuses a sign-in code or the provider fails: the courier hook answers non-2xx and Kratos retries the message up to its courier retry limit; the member can ask for a new code. Worker down or `BIRGE_INTERNAL_ADDR` unset while Kratos uses the hook: no codes are sent until it is back (Kratos keeps retrying).
- Database slow or down while recording the activity day: logged as `membership.MemberActiveDay` with the member ID, the request continues (the recording has its own 2 s deadline); the next request retries because failures are not cached. The member middleware has already read the member at that point, so a dead database fails the request there, not here.
- Encryptor unavailable: profile updates that set a name fail; reads that need Open fail.
- Kratos admin API down during deletion: the member data is already erased; `membership.DeleteIdentity` retries until Kratos answers, and the tombstone refuses the identity meanwhile.

## Tests

- Unit: `service/service_test.go`, `service/reference_test.go` (deactivated district and needs kept by the member, refused as new choices), `service/deletion_test.go` (tombstone refuses the identity, retries), `service/activity_test.go` (once per day in the deployment zone, cache, store failures never block, replicas, payload against the event schema), `model/model_test.go`, `authgw/authgw_test.go`, `authgw/courier_test.go` (secret, templates, numbers, guard refusal → non-2xx, no code/number/secret in logs), `api/server_test.go` — lazy create, idempotent children/verification/blocks, authgw PoW and rate limits, HTTP mapping.
- Integration: `adapter/postgres_integration_test.go` — PostgreSQL store + outbox with `testinfra.Postgres` (including concurrent replicas recording one active day).
- Integration: `adapter/reference_integration_test.go` — reference sync: existing IDs kept, stable IDs for new rows, idempotent reseed, deactivation and reactivation, concurrent migrate jobs, refused seeds.
- Wiring: `internal/wiring/migrate_seed_test.go` — `birge migrate` with the default sets, a second migrate, dropping and restoring the DEMO set with a member on a DEMO district; `presets/reference_test.go` — kk/ru/en names, unique codes and stable IDs across sets, the Kazakhstan division.
- Wiring: `internal/wiring/moderation_e2e_test.go` — verification approve and suspension after `block_author`; `internal/wiring/courier_test.go` — courier hook through notify's sign-in guard and the log sender.

Suspension (moderation `block_author`) refuses every member route with 403 `member_suspended`, except the crisis and own-data routes (`crisisExempt` in wiring): help request, raising and closing her SOS, the emergency plan, consents and the deletion request (non-negotiable 1). `CrisisMemberID` resolves her for those.
