-- name: InsertBlock :execrows
INSERT INTO membership.blocks (blocker, blocked, since)
VALUES (@blocker, @blocked, @since)
ON CONFLICT (blocker, blocked) DO NOTHING;

-- name: DeleteBlock :exec
DELETE FROM membership.blocks
WHERE blocker = @blocker AND blocked = @blocked;

-- name: CountBlocks :one
SELECT count(*) FROM membership.blocks
WHERE blocker = @blocker;

-- name: ListBlocks :many
SELECT b.blocked, b.since, m.display_name
FROM membership.blocks b
JOIN membership.members m ON m.id = b.blocked
WHERE b.blocker = @blocker
ORDER BY b.since DESC, b.blocked
LIMIT @max_rows;

-- A block either way, or either member suspended (mutual invisibility, ADR-0019).
-- name: IsBlocked :one
SELECT (
    EXISTS (
        SELECT 1 FROM membership.blocks
        WHERE (blocker = @a AND blocked = @b) OR (blocker = @b AND blocked = @a)
    )
    OR EXISTS (
        SELECT 1 FROM membership.members
        WHERE id IN (@a, @b) AND suspended_at IS NOT NULL
    )
) AS blocked;
