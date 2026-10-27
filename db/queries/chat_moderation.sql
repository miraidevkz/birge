-- name: GetRestriction :one
SELECT room_id, member_id, banned_at, muted_until, updated_at
FROM chat.room_restrictions
WHERE room_id = @room_id AND member_id = @member_id;

-- name: SetBan :exec
INSERT INTO chat.room_restrictions (room_id, member_id, banned_at, updated_at)
VALUES (@room_id, @member_id, @banned_at, @updated_at)
ON CONFLICT (room_id, member_id) DO UPDATE
SET banned_at = EXCLUDED.banned_at, updated_at = EXCLUDED.updated_at;

-- name: SetMutedUntil :exec
INSERT INTO chat.room_restrictions (room_id, member_id, muted_until, updated_at)
VALUES (@room_id, @member_id, @muted_until, @updated_at)
ON CONFLICT (room_id, member_id) DO UPDATE
SET muted_until = EXCLUDED.muted_until, updated_at = EXCLUDED.updated_at;

-- name: UpsertRoomMentor :exec
INSERT INTO chat.room_mentors (room_id, member_id, assigned_by, assigned_at)
VALUES (@room_id, @member_id, @assigned_by, @assigned_at)
ON CONFLICT (room_id, member_id) DO UPDATE
SET assigned_by = EXCLUDED.assigned_by, assigned_at = EXCLUDED.assigned_at, removed_at = NULL;

-- name: RemoveRoomMentor :execrows
UPDATE chat.room_mentors
SET removed_at = @removed_at
WHERE room_id = @room_id AND member_id = @member_id AND removed_at IS NULL;

-- name: IsRoomMentor :one
SELECT EXISTS (
    SELECT 1 FROM chat.room_mentors
    WHERE room_id = @room_id AND member_id = @member_id AND removed_at IS NULL
)::boolean;

-- name: InsertReport :one
-- Does nothing (no row) when the member already reported the message.
INSERT INTO chat.reports (id, message_id, room_id, reporter_id, reason, created_at)
VALUES (@id, @message_id, @room_id, @reporter_id, @reason, @created_at)
ON CONFLICT (message_id, reporter_id) DO NOTHING
RETURNING id;

-- name: CloseReports :execrows
UPDATE chat.reports
SET closed_at = @closed_at
WHERE message_id = @message_id AND closed_at IS NULL;

-- name: ChatSchemaExists :one
SELECT EXISTS (
    SELECT 1 FROM information_schema.schemata WHERE schema_name = 'chat'
) AS present;

-- name: ChatRoomsEmpty :one
SELECT NOT EXISTS (SELECT 1 FROM chat.rooms) AS empty;
