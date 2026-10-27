-- name: InsertDeletionRequest :one
INSERT INTO privacy.deletion_requests (member_id, requested_at, due_at)
VALUES (@member_id, @requested_at, @due_at)
ON CONFLICT (member_id) DO NOTHING
RETURNING member_id, requested_at, due_at, completed_at;

-- name: GetDeletionRequest :one
SELECT member_id, requested_at, due_at, completed_at
FROM privacy.deletion_requests
WHERE member_id = @member_id;

-- name: MarkDeletionCompleted :one
UPDATE privacy.deletion_requests
SET completed_at = @completed_at
WHERE member_id = @member_id
  AND completed_at IS NULL
RETURNING member_id, requested_at, due_at, completed_at;
