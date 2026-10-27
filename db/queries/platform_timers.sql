-- name: InsertTimer :execrows
INSERT INTO platform.timers (kind, subject_id, dedup_key, due_at, payload)
VALUES (@kind, @subject_id, @dedup_key, @due_at, @payload)
ON CONFLICT (kind, dedup_key) WHERE done_at IS NULL AND dedup_key IS NOT NULL DO NOTHING;

-- name: ClaimDueTimer :one
SELECT id, kind, subject_id, dedup_key, due_at, payload, attempts
FROM platform.timers
WHERE done_at IS NULL AND due_at <= @now
ORDER BY due_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: CompleteTimer :exec
UPDATE platform.timers
SET done_at = @done_at
WHERE id = @id;

-- name: RescheduleTimer :exec
UPDATE platform.timers
SET attempts = attempts + 1, due_at = @due_at, last_error = @last_error
WHERE id = @id AND done_at IS NULL;

-- name: CancelTimers :execrows
UPDATE platform.timers
SET done_at = @done_at, last_error = 'cancelled'
WHERE kind = @kind AND subject_id = @subject_id AND done_at IS NULL;

-- name: OldestOverdueTimer :one
SELECT min(due_at)::timestamptz AS oldest
FROM platform.timers
WHERE done_at IS NULL AND due_at <= @now;
