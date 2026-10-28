-- name: HitRateCounter :execrows
-- One atomic statement per hit: the first hit of a window inserts the row,
-- later hits add one while the window holds fewer than max_hits. Zero rows
-- affected means the limit is reached; a refused hit is not counted.
-- Concurrent hits on one key serialize on the row lock.
INSERT INTO platform.rate_counters (key, window_start, hits, expires_at)
VALUES (@key, @window_start, 1, @expires_at)
ON CONFLICT (key, window_start) DO UPDATE
SET hits = platform.rate_counters.hits + 1
WHERE platform.rate_counters.hits < @max_hits::bigint;

-- name: ClaimOnce :execrows
-- One row affected: the key was free (never claimed, or its claim expired)
-- and is now claimed until expires_at. Zero: a live claim exists.
INSERT INTO platform.one_time_claims (key, expires_at)
VALUES (@key, @expires_at)
ON CONFLICT (key) DO UPDATE
SET expires_at = excluded.expires_at
WHERE platform.one_time_claims.expires_at <= @now::timestamptz;

-- name: DeleteExpiredRateCounters :execrows
-- A bounded batch; SKIP LOCKED lets several sweepers (worker replicas) and
-- live hits run at the same time without waiting on each other.
DELETE FROM platform.rate_counters
WHERE (key, window_start) IN (
    SELECT c.key, c.window_start FROM platform.rate_counters c
    WHERE c.expires_at <= @now
    LIMIT @batch
    FOR UPDATE SKIP LOCKED
);

-- name: DeleteExpiredClaims :execrows
DELETE FROM platform.one_time_claims
WHERE key IN (
    SELECT c.key FROM platform.one_time_claims c
    WHERE c.expires_at <= @now
    LIMIT @batch
    FOR UPDATE SKIP LOCKED
);
