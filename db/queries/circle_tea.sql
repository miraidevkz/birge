-- Account deletion. Matches stay as tombstones for the other member's
-- history and keep the two requests they reference (foreign keys); every
-- other request of the member is deleted.

-- name: ListMemberTeaRequestIDs :many
SELECT id FROM circle.tea_requests
WHERE member_id = @member_id
ORDER BY id
FOR UPDATE;

-- name: ListMemberMatchIDs :many
SELECT id FROM circle.matches
WHERE member_a = @member_id OR member_b = @member_id
ORDER BY id
FOR UPDATE;

-- name: DeleteUnmatchedTeaRequests :execrows
DELETE FROM circle.tea_requests r
WHERE r.member_id = @member_id
  AND NOT EXISTS (
    SELECT 1 FROM circle.matches m WHERE m.request_a_id = r.id OR m.request_b_id = r.id
  );

-- name: CancelMatchedTeaRequests :execrows
UPDATE circle.tea_requests
SET status = 'cancelled'
WHERE member_id = @member_id AND status = 'open';

-- A proposal she can no longer answer ends for the other member.
-- name: ExpireMemberProposedMatches :execrows
UPDATE circle.matches
SET status = 'expired'
WHERE (member_a = @member_id OR member_b = @member_id) AND status = 'proposed';

-- name: DeleteMemberAcquaintances :execrows
DELETE FROM circle.acquaintances
WHERE member_a = @member_id OR member_b = @member_id;

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
