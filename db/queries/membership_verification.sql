-- InsertPendingVerificationRequest returns no row while a request is pending.
-- name: InsertPendingVerificationRequest :one
INSERT INTO membership.verification_requests (member_id, status, created_at, updated_at)
VALUES (@member_id, 'pending', @now, @now)
ON CONFLICT (member_id) WHERE status = 'pending' DO NOTHING
RETURNING id;

-- name: GetVerificationRequest :one
SELECT id, member_id, status, reject_reason, created_at, updated_at
FROM membership.verification_requests
WHERE id = @id;

-- name: GetVerificationRequestForUpdate :one
SELECT id, member_id, status, reject_reason, created_at, updated_at
FROM membership.verification_requests
WHERE id = @id
FOR UPDATE;

-- name: DecideVerificationRequest :execrows
UPDATE membership.verification_requests
SET status = @status,
    reject_reason = @reject_reason,
    updated_at = @now
WHERE id = @id AND status = 'pending';
