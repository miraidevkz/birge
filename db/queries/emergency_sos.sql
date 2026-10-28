-- name: AdvisoryXactLock :exec
-- Serialises writes that check a per-member limit (SOS per hour, trusted
-- contacts, plan sends) until the transaction ends.
SELECT pg_advisory_xact_lock(hashtextextended(@scope_key::text, 0));

-- Account deletion (the only deletion path of this context, AGENTS.md N6).

-- name: ListMemberSosIDs :many
SELECT id FROM emergency.sos_requests
WHERE member_id = @member_id
ORDER BY id
FOR UPDATE;

-- Responses to her requests and her own responses to other requests.
-- name: DeleteMemberSosResponses :execrows
DELETE FROM emergency.sos_responses
WHERE sos_id = ANY(@sos_ids::uuid[]) OR responder_id = @member_id;

-- name: DeleteMemberSos :execrows
DELETE FROM emergency.sos_requests WHERE member_id = @member_id;

-- name: DeleteMemberChildCard :execrows
DELETE FROM emergency.child_cards WHERE member_id = @member_id;

-- Her own contacts and the entries in which she is another owner's contact.
-- name: DeleteMemberTrustedContacts :execrows
DELETE FROM emergency.trusted_contacts
WHERE owner_id = @member_id OR contact_member_id = @member_id;

-- name: DeleteMemberPlanSends :execrows
DELETE FROM emergency.plan_sends WHERE member_id = @member_id;

-- name: CountSosSince :one
SELECT count(*)::integer
FROM emergency.sos_requests
WHERE member_id = @member_id
  AND created_at > @since;

-- name: InsertSos :one
-- Does nothing (no row) when the member already used this Idempotency-Key.
INSERT INTO emergency.sos_requests (id, member_id, idempotency_key, type, content, status, created_at, closes_at)
VALUES (@id, @member_id, @idempotency_key, @type, @content, 'open', @created_at, @closes_at)
ON CONFLICT (member_id, idempotency_key) DO NOTHING
RETURNING id, member_id, idempotency_key, type, content, status, created_at, closes_at, district_id, shared_at, closed_at, close_reason, seen_at, seen_by, closed_by;

-- name: GetSosByIdempotencyKey :one
SELECT id, member_id, idempotency_key, type, content, status, created_at, closes_at, district_id, shared_at, closed_at, close_reason, seen_at, seen_by, closed_by
FROM emergency.sos_requests
WHERE member_id = @member_id
  AND idempotency_key = @idempotency_key;

-- name: GetSos :one
SELECT id, member_id, idempotency_key, type, content, status, created_at, closes_at, district_id, shared_at, closed_at, close_reason, seen_at, seen_by, closed_by
FROM emergency.sos_requests
WHERE id = @id;

-- name: GetSosForUpdate :one
SELECT id, member_id, idempotency_key, type, content, status, created_at, closes_at, district_id, shared_at, closed_at, close_reason, seen_at, seen_by, closed_by
FROM emergency.sos_requests
WHERE id = @id
FOR UPDATE;

-- name: CountSosResponses :one
SELECT count(*)::integer
FROM emergency.sos_responses
WHERE sos_id = @sos_id;

-- name: ListMySos :many
SELECT s.id, s.member_id, s.idempotency_key, s.type, s.content, s.status, s.created_at, s.closes_at,
       s.district_id, s.shared_at, s.closed_at, s.close_reason, s.seen_at, s.seen_by, s.closed_by,
       (SELECT count(*) FROM emergency.sos_responses r WHERE r.sos_id = s.id)::integer AS response_count
FROM emergency.sos_requests s
WHERE s.member_id = @member_id
ORDER BY s.created_at DESC, s.id DESC
LIMIT @page_limit;

-- name: ShareSos :one
-- Never shares a childcare request (N5); the table check enforces it too.
UPDATE emergency.sos_requests
SET district_id = @district_id, shared_at = @shared_at
WHERE id = @id
  AND status = 'open'
  AND shared_at IS NULL
  AND type <> 'childcare'
RETURNING id, member_id, idempotency_key, type, content, status, created_at, closes_at, district_id, shared_at, closed_at, close_reason, seen_at, seen_by, closed_by;

-- name: CloseSos :one
UPDATE emergency.sos_requests
SET status = 'closed', closed_at = @closed_at, close_reason = @close_reason,
    closed_by = sqlc.narg('closed_by')::uuid
WHERE id = @id
  AND status = 'open'
RETURNING id, member_id, idempotency_key, type, content, status, created_at, closes_at, district_id, shared_at, closed_at, close_reason, seen_at, seen_by, closed_by;

-- name: ListSosFeed :many
-- The district feed: open, shared, not expired, never childcare (N5), never
-- the viewer's own. Blocked members are filtered by the service (membership
-- owns blocks; no cross-context join).
SELECT s.id, s.member_id, s.type, s.content, s.created_at,
       EXISTS (
         SELECT 1 FROM emergency.sos_responses r
         WHERE r.sos_id = s.id AND r.responder_id = @viewer_id
       ) AS responded_by_me
FROM emergency.sos_requests s
WHERE s.district_id = @district_id
  AND s.status = 'open'
  AND s.shared_at IS NOT NULL
  AND s.type <> 'childcare'
  AND s.member_id <> @viewer_id
  AND s.closes_at > @now
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (s.created_at, s.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY s.created_at DESC, s.id DESC
LIMIT @page_limit;

-- name: InsertSosResponse :one
-- Does nothing (no row) when the member already responded.
INSERT INTO emergency.sos_responses (sos_id, responder_id, created_at)
VALUES (@sos_id, @responder_id, @created_at)
ON CONFLICT (sos_id, responder_id) DO NOTHING
RETURNING id, sos_id, responder_id, created_at, accepted_at;

-- name: ListSosResponses :many
SELECT id, sos_id, responder_id, created_at, accepted_at
FROM emergency.sos_responses
WHERE sos_id = @sos_id
ORDER BY created_at ASC, id ASC
LIMIT @page_limit;

-- name: GetSosResponse :one
SELECT id, sos_id, responder_id, created_at, accepted_at
FROM emergency.sos_responses
WHERE id = @id;

-- name: AcceptSosResponse :one
UPDATE emergency.sos_responses
SET accepted_at = @accepted_at
WHERE id = @id
  AND accepted_at IS NULL
RETURNING id, sos_id, responder_id, created_at, accepted_at;

-- name: SosRespondedBy :one
SELECT EXISTS (
  SELECT 1 FROM emergency.sos_responses
  WHERE sos_id = @sos_id AND responder_id = @responder_id
);

-- name: MarkSosSeen :one
-- Keeps the first moderator: a later "Seen" changes nothing (no row).
UPDATE emergency.sos_requests
SET seen_at = @seen_at, seen_by = @seen_by
WHERE id = @id
  AND seen_at IS NULL
RETURNING id, member_id, idempotency_key, type, content, status, created_at, closes_at, district_id, shared_at, closed_at, close_reason, seen_at, seen_by, closed_by;

-- name: ListStaffOpenSos :many
-- Moderator queue, open requests oldest first (the longest waiting on top),
-- keyset by (created_at, id). Every type, childcare included: moderators
-- are among the few who see it (N5).
SELECT s.id, s.member_id, s.idempotency_key, s.type, s.content, s.status, s.created_at, s.closes_at,
       s.district_id, s.shared_at, s.closed_at, s.close_reason, s.seen_at, s.seen_by, s.closed_by,
       (SELECT count(*) FROM emergency.sos_responses r WHERE r.sos_id = s.id)::integer AS response_count
FROM emergency.sos_requests s
WHERE s.status = 'open'
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (s.created_at, s.id) > (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY s.created_at ASC, s.id ASC
LIMIT @page_limit;

-- name: ListStaffClosedSos :many
-- Moderator queue, closed requests newest first, keyset by (created_at, id).
SELECT s.id, s.member_id, s.idempotency_key, s.type, s.content, s.status, s.created_at, s.closes_at,
       s.district_id, s.shared_at, s.closed_at, s.close_reason, s.seen_at, s.seen_by, s.closed_by,
       (SELECT count(*) FROM emergency.sos_responses r WHERE r.sos_id = s.id)::integer AS response_count
FROM emergency.sos_requests s
WHERE s.status = 'closed'
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (s.created_at, s.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY s.created_at DESC, s.id DESC
LIMIT @page_limit;
