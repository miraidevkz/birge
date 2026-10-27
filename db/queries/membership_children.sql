-- name: ListChildren :many
SELECT id, birth_year, needs
FROM membership.children
WHERE member_id = @member_id
ORDER BY created_at, id;

-- name: CountChildren :one
SELECT count(*) FROM membership.children
WHERE member_id = @member_id;

-- name: GetChildByIdempotencyKey :one
SELECT id, birth_year, needs
FROM membership.children
WHERE member_id = @member_id AND idempotency_key = @idempotency_key;

-- name: InsertChild :one
INSERT INTO membership.children (member_id, idempotency_key, birth_year, needs, created_at, updated_at)
VALUES (@member_id, @idempotency_key, @birth_year, @needs, @now, @now)
RETURNING id, birth_year, needs;

-- name: UpdateChild :one
UPDATE membership.children
SET birth_year = @birth_year, needs = @needs, updated_at = @now
WHERE id = @id AND member_id = @member_id
RETURNING id, birth_year, needs;

-- name: DeleteChild :execrows
DELETE FROM membership.children
WHERE id = @id AND member_id = @member_id;
