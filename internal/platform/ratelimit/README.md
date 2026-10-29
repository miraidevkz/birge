# platform/ratelimit

Fixed-window rate limits and one-time claims that hold across replicas, so that two or more API (or worker) replicas behind the ingress behave like one ([ADR-0011](../../../docs/adr/0011-kubernetes-first.md)). Used by every limit in the backend:

| Limit | Limiter name (key prefix) | Where | On a store error |
|---|---|---|---|
| `/v1` per IP (`BIRGE_API_RATE_PER_IP`) | `api.ip` | `wiring.NewAPIHandler`, `Middleware` | fail open |
| plan link view per IP (`BIRGE_PLAN_LINK_RATE_PER_IP`) | `plan_link.ip` | `wiring.NewAPIHandler`, `Middleware` | fail open |
| auth gateway per IP / per number / global (`BIRGE_AUTH_RATE_*`) | `auth.ip`, `auth.number`, `auth.global` | `membership/authgw` | fail closed (503) |
| PoW replay (one use per solution) | `auth.pow` (claim) | `membership/authgw` | fail closed (503) |
| notify SMS guard: per number, global, daily budget (`BIRGE_SMS_*`) | `notify.number`, `notify.global`, `notify.day` | worker, `notify/sms/guard` | ordinary SMS refused, critical SMS sent |
| sign-in SMS guard, same settings, own counters | `signin.number`, `signin.global`, `signin.day` | worker, `notify/sms/guard` | sign-in SMS refused |

The worker can run as several replicas (JetStream consumers and `SKIP LOCKED` timers already share work), so the SMS guards are on the same store: with in-memory guards the per-number limit and the daily budget would multiply by the number of workers.

## Owns

| Table (schema `platform`) | Purpose | Personal data |
|---|---|---|
| `rate_counters` | one row per key and fixed window: `hits`, `expires_at` | none: `key` is `HMAC-SHA-256(BIRGE_HASH_KEY_FILE, "birge.ratelimit.v1\|<limiter>\|<key>")` truncated to 128 bits; the key may be an IP or a phone number, the table never holds either |
| `one_time_claims` | one row per claimed key until `expires_at` (PoW challenges) | none, hashed the same way |

## Design

- **Fixed windows.** A `Rate{Burst, Every}` (configured as `<burst>/<interval>`) allows `Burst` hits per window of `Burst × Every`: the same average rate as the token buckets used before, with at most `Burst` at once. Windows are aligned to the Unix epoch, so every replica computes the same window and a day-long window is the UTC day. Near a window boundary a client can get up to twice the burst in a short time; these limits protect capacity and SMS providers, and that is acceptable. A refused hit is not counted.
- **One statement per hit.** `HitRateCounter` is a single `INSERT ... ON CONFLICT (key, window_start) DO UPDATE SET hits = hits + 1 WHERE hits < max`. The window start is in the primary key, so a new window is a new row and nothing is ever reset in place; concurrent hits on one key serialize on its row lock. `ClaimOnce` is the same shape on `one_time_claims` and succeeds only for a new key or over an expired claim. Both run on the pool, outside any request transaction, so a rolled-back request still counts.
- **Regular (logged) tables, not `UNLOGGED`.** Losing rate counters in a crash would only reset some windows, which is fine for capacity limits. It is not fine for the rest of what lives here: an `UNLOGGED` table is truncated on crash recovery and is not replicated, so after a failover to a CloudNativePG standby it is empty. A PoW solution issued in the five minutes before could then be used again on any replica, and the SMS daily budget, a cost breaker against pumping, would start the day again. `psqldef` also cannot round-trip `CREATE UNLOGGED TABLE` (it generates a destructive `DROP CONSTRAINT`, see `docs/solved-problems.md`), so the declarative schema could not express it. The cost of logging is one small row write per limited request; at pilot volume the WAL is negligible. If it ever shows up in WAL volume, `rate_counters` alone can be switched with a reviewed hand-written `ALTER TABLE ... SET UNLOGGED`, keeping `one_time_claims` logged and moving the daily budget to it.
- **Cleanup by a sweeper loop.** Expired rows are never read as live (a counter's key contains its window, a claim checks its expiry), so cleanup only bounds the table size. `Sweeper` runs in the worker every minute and deletes expired rows in batches of 1000 with `FOR UPDATE SKIP LOCKED`, so several worker replicas and live hits never wait on each other. It is a plain loop like the outbox relay, not a platform timer: it changes no domain state, and a timer every minute would leave a done row in `platform.timers` each time. If no worker runs, the tables only grow until it is back.
- **Memory store.** `MemoryStore` has the same semantics in process memory, for tests and a single replica. With `BIRGE_RATE_LIMIT_STORE=memory` wiring passes no store, and every limiter, the PoW check and each SMS guard keep a private table of at most 100 000 keys, so a flood on one limit cannot fill the table of another. A full table refuses the new key (`ErrStoreFull`) instead of evicting a live one; the caller's failure policy then applies.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `BIRGE_RATE_LIMIT_STORE` | `postgres` | `postgres`: shared store, required with more than one API or worker replica. `memory`: per process, tests and single-replica development only (every limit is multiplied by the replica count) |
| `BIRGE_HASH_KEY_FILE` | required on the API; on the worker with `postgres` | HMAC key of the stored keys (shared with the auth gateway's log hashes) |

The limits themselves are configured where they are used (`docs/development.md`).

## Invariants

- No IP or phone number is stored: only the keyed hash (`PostgresStore.hash`; `postgres_integration_test.go` checks the tables).
- A limit holds across replicas: concurrent hits from two stores on one database never exceed it (`TestPostgresStoreSharedAcrossReplicas`; `wiring.TestTwoReplicasShareLimitsAndPoW` end to end).
- A claim is taken at most once while it lives, on any replica (same tests).
- The sweeper deletes only expired rows (`TestSweeperDeletesOnlyExpired`).

## Failure modes

- Database down or slow: `Hit`/`Claim` return the error. `Middleware` lets the request through and logs `RateLimitStoreError` (limiter name only); the auth gateway answers 503 `auth_unavailable` and logs `AuthLimitStoreError`; the SMS guard refuses ordinary SMS (`ErrUnavailable`) and sends critical ones, logging `SMSGuardStoreError`.
- Clock skew between replicas: a replica a few seconds off counts the last seconds of a window into the next one. Harmless at these window lengths.
- Sweeper failure: logged as `RateLimitSweepFailed` and retried at the next tick; it never stops the worker.

## Tests

- Unit: `ratelimit_test.go`: fixed windows, refused hits not counted, bounded memory store, claims, `RetryAfter`, UTC-day windows, `Middleware` (429, fail open without logging the key).
- Integration: `postgres_integration_test.go` (throwaway PostgreSQL): two stores share a limit under concurrency, claims across stores, hashed keys, sweeper.
- End to end: `internal/wiring/api_integration_test.go` `TestTwoReplicasShareLimitsAndPoW`: two API handlers with their own pools on one database share the `/v1` limit and reject a replayed PoW solution; with the memory store they do not.
