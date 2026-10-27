-- name: InsertDutyShift :one
-- Does nothing (no row) when a shift with this id (Idempotency-Key) exists.
INSERT INTO safety.duty_shifts (id, staff_id, role, starts_at, ends_at, backup, phone, created_at)
VALUES (@id, @staff_id, @role, @starts_at, @ends_at, @backup, @phone, @created_at)
ON CONFLICT (id) DO NOTHING
RETURNING id, staff_id, role, starts_at, ends_at, backup, phone, created_at, cancelled_at;

-- name: GetDutyShift :one
SELECT id, staff_id, role, starts_at, ends_at, backup, phone, created_at, cancelled_at
FROM safety.duty_shifts
WHERE id = @id;

-- name: ListDutyShifts :many
SELECT id, staff_id, role, starts_at, ends_at, backup, phone, created_at, cancelled_at
FROM safety.duty_shifts
WHERE cancelled_at IS NULL
  AND starts_at < @period_end
  AND ends_at > @period_start
ORDER BY starts_at, id
LIMIT @page_limit;

-- name: ListCoveringShifts :many
SELECT id, staff_id, role, starts_at, ends_at, backup, phone, created_at, cancelled_at
FROM safety.duty_shifts
WHERE cancelled_at IS NULL
  AND role = @role
  AND backup = @backup
  AND starts_at <= @at
  AND ends_at > @at
ORDER BY starts_at, id;

-- name: CancelDutyShift :one
-- Only a shift that has not started can be cancelled.
UPDATE safety.duty_shifts
SET cancelled_at = @cancelled_at
WHERE id = @id
  AND cancelled_at IS NULL
  AND starts_at > @cancelled_at
RETURNING id, staff_id, role, starts_at, ends_at, backup, phone, created_at, cancelled_at;
