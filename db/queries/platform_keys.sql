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

-- name: RewrapMemberKey :execrows
-- KEK rotation: replaces the wrapping of a live data key, only while it is
-- still wrapped by from_kek_id. A concurrent rewrap (another replica) or a
-- shred that got there first wins, and this statement changes nothing.
UPDATE platform.member_keys
SET wrapped_dek = @wrapped_dek, kek_id = @kek_id
WHERE member_id = @member_id AND kek_id = @from_kek_id AND destroyed_at IS NULL;
