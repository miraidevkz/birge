-- name: InsertTeaRequest :one
-- Does nothing (no row) when the member already used this Idempotency-Key.
-- A second open request for the same member hits tea_requests_one_open_uidx.
INSERT INTO circle.tea_requests (
    id, member_id, idempotency_key, format, same_district, similar_age, shared_needs,
    status, created_at, expires_at
)
VALUES (
    @id, @member_id, @idempotency_key, @format, @same_district, @similar_age, @shared_needs,
    'open', @created_at, @expires_at
)
ON CONFLICT (member_id, idempotency_key) DO NOTHING
RETURNING id, member_id, idempotency_key, format, same_district, similar_age, shared_needs,
          status, created_at, expires_at;

-- name: GetTeaRequestByIdempotencyKey :one
SELECT id, member_id, idempotency_key, format, same_district, similar_age, shared_needs,
       status, created_at, expires_at
FROM circle.tea_requests
WHERE member_id = @member_id
  AND idempotency_key = @idempotency_key;

-- name: GetTeaRequest :one
SELECT id, member_id, idempotency_key, format, same_district, similar_age, shared_needs,
       status, created_at, expires_at
FROM circle.tea_requests
WHERE id = @id;

-- name: GetTeaRequestForUpdate :one
SELECT id, member_id, idempotency_key, format, same_district, similar_age, shared_needs,
       status, created_at, expires_at
FROM circle.tea_requests
WHERE id = @id
FOR UPDATE;

-- name: ListTeaRequestsByMember :many
SELECT id, member_id, idempotency_key, format, same_district, similar_age, shared_needs,
       status, created_at, expires_at
FROM circle.tea_requests
WHERE member_id = @member_id
ORDER BY created_at DESC, id DESC
LIMIT @page_limit;

-- name: ListOpenTeaRequests :many
SELECT id, member_id, idempotency_key, format, same_district, similar_age, shared_needs,
       status, created_at, expires_at
FROM circle.tea_requests
WHERE status = 'open'
  AND expires_at > @now
ORDER BY created_at ASC, id ASC;

-- name: MarkTeaRequestMatched :one
UPDATE circle.tea_requests
SET status = 'matched'
WHERE id = @id
  AND status = 'open'
RETURNING id, member_id, idempotency_key, format, same_district, similar_age, shared_needs,
          status, created_at, expires_at;

-- name: CancelTeaRequest :one
UPDATE circle.tea_requests
SET status = 'cancelled'
WHERE id = @id
  AND member_id = @member_id
  AND status = 'open'
RETURNING id, member_id, idempotency_key, format, same_district, similar_age, shared_needs,
          status, created_at, expires_at;

-- name: ExpireTeaRequest :one
UPDATE circle.tea_requests
SET status = 'expired'
WHERE id = @id
  AND status = 'open'
RETURNING id, member_id, idempotency_key, format, same_district, similar_age, shared_needs,
          status, created_at, expires_at;

-- name: HasOpenTeaRequest :one
SELECT EXISTS (
    SELECT 1 FROM circle.tea_requests
    WHERE member_id = @member_id AND status = 'open'
)::boolean AS has_open;
