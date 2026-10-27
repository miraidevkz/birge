-- InsertPendingVerificationRequest returns no row while a request is pending.
-- name: InsertPendingVerificationRequest :one
INSERT INTO membership.verification_requests (member_id, status, created_at, updated_at)
VALUES (@member_id, 'pending', @now, @now)
ON CONFLICT (member_id) WHERE status = 'pending' DO NOTHING
RETURNING id;
