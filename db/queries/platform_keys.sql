-- name: InsertMemberKey :exec
-- Concurrent first writers race here; the loser re-reads the winner's key.
INSERT INTO platform.member_keys (member_id, wrapped_dek, kek_id)
VALUES (@member_id, @wrapped_dek, @kek_id)
ON CONFLICT (member_id) DO NOTHING;

-- name: GetMemberKey :one
SELECT member_id, wrapped_dek, kek_id, created_at, destroyed_at
FROM platform.member_keys
WHERE member_id = @member_id;

-- name: ShredMemberKey :exec
-- Idempotent: keeps the first destruction time; creates a tombstone when the
-- member never had a key, so a later Seal cannot create one.
INSERT INTO platform.member_keys (member_id, wrapped_dek, kek_id, destroyed_at)
VALUES (@member_id, ''::bytea, '', @destroyed_at)
ON CONFLICT (member_id) DO UPDATE
SET wrapped_dek = ''::bytea,
    destroyed_at = coalesce(platform.member_keys.destroyed_at, excluded.destroyed_at);
