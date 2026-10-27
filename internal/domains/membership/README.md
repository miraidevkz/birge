# membership

Members, their children, verification requests, blocks and the reference data (cities, districts, needs). Tables: `db/schema/100_membership.sql`. HTTP: tag `membership` in `contracts/openapi/birge.yaml`. The auth gateway (`authgw/`) is a separate part of this context.

## Invariants

- A member is created lazily on the first authenticated request of an identity (ADR-0008 addendum). `identity_id` is unique; concurrent first calls create one member and one `membership.MemberRegistered`.
- A member only ever reads or changes her own data; the principal comes from `internal/platform/auth`. Staff sessions are refused. Reference endpoints are anonymous.
- The display name is personal data: sealed through `port.Encryptor` with the member's key (ADR-0009), never logged, never in events. Children carry no names.
- Verification: at most one pending request per member; repeated requests while pending have one effect and one `membership.VerificationRequested`.
- Blocks (ADR-0019): idempotent, no self-block, mutual invisibility. Other contexts filter with `service.IsBlocked(ctx, a, b)`, which is symmetric.

## Events

| Event | When |
|---|---|
| `membership.MemberRegistered` v1 | first request of a new identity |
| `membership.VerificationRequested` v1 | a member asks for a verification call and none is pending |

## For other contexts

`service.Service.IsBlocked` and `service.Service.Member`. They do no authorization; callers decide what they may show.

## Not yet

- Production `Encryptor` (comes with the platform crypto package) and wiring in `cmd/birge`.
- The moderator side of verification (`membership.MemberVerified`).
