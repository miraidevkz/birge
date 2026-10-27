-- name: InsertSpecialist :one
INSERT INTO directory.specialists (
    id, added_by, idempotency_key, name, profession, district_id, needs, contacts, status, created_at
) VALUES (
    @id, @added_by, @idempotency_key, @name, @profession, @district_id, @needs, @contacts, 'pending', @created_at
)
ON CONFLICT (added_by, idempotency_key) DO NOTHING
RETURNING id, added_by, idempotency_key, name, profession, district_id, needs, contacts,
          status, reject_reason, created_at;

-- name: GetSpecialistByIdempotencyKey :one
SELECT id, added_by, idempotency_key, name, profession, district_id, needs, contacts,
       status, reject_reason, created_at
FROM directory.specialists
WHERE added_by = @added_by
  AND idempotency_key = @idempotency_key;

-- name: GetSpecialist :one
SELECT id, added_by, idempotency_key, name, profession, district_id, needs, contacts,
       status, reject_reason, created_at
FROM directory.specialists
WHERE id = @id;

-- name: GetSpecialistForUpdate :one
SELECT id, added_by, idempotency_key, name, profession, district_id, needs, contacts,
       status, reject_reason, created_at
FROM directory.specialists
WHERE id = @id
FOR UPDATE;

-- name: SearchActiveSpecialists :many
SELECT id, added_by, idempotency_key, name, profession, district_id, needs, contacts,
       status, reject_reason, created_at
FROM directory.specialists
WHERE status = 'active'
  AND (sqlc.narg(filter_profession)::text IS NULL OR profession = sqlc.narg(filter_profession))
  AND (sqlc.narg(filter_district)::uuid IS NULL OR district_id = sqlc.narg(filter_district))
  AND (sqlc.narg(filter_need)::text IS NULL OR needs @> ARRAY[sqlc.narg(filter_need)]::text[])
  AND (
    sqlc.narg(cursor_created_at)::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT @page_limit;

-- name: ApproveSpecialist :one
UPDATE directory.specialists
SET status = 'active', reject_reason = NULL
WHERE id = @id
  AND status = 'pending'
RETURNING id, added_by, idempotency_key, name, profession, district_id, needs, contacts,
          status, reject_reason, created_at;

-- name: RejectSpecialist :one
UPDATE directory.specialists
SET status = 'rejected', reject_reason = @reject_reason
WHERE id = @id
  AND status = 'pending'
RETURNING id, added_by, idempotency_key, name, profession, district_id, needs, contacts,
          status, reject_reason, created_at;
