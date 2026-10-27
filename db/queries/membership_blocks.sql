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
WHERE b.blocker = @blocker AND m.deleted_at IS NULL
ORDER BY b.since DESC, b.blocked
LIMIT @max_rows;

-- Account deletion: blocks in both directions go with the member.
-- name: DeleteMemberBlocks :execrows
DELETE FROM membership.blocks
WHERE blocker = @member_id OR blocked = @member_id;

-- A block either way, or either member suspended (mutual invisibility, ADR-0019).
-- A deleted member is not "blocked": her tombstones (chat "message deleted")
-- stay visible to the others, and she has no blocks left.
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
