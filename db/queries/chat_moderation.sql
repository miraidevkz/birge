-- Account deletion. Her messages become "message deleted" tombstones (as
-- retention does) so per-room sequences and the other members' history stay
-- whole; messages under legal hold (ADR-0019) are left untouched. Direct
-- rooms stay: the other member keeps the conversation.

-- name: ListMemberHeldMessageIDs :many
SELECT id FROM chat.messages
WHERE author_id = @member_id AND legal_hold
ORDER BY id;

-- name: ListMemberPurgeableMessageIDs :many
SELECT id FROM chat.messages
WHERE author_id = @member_id AND NOT legal_hold AND purged_at IS NULL
ORDER BY id
FOR UPDATE;

-- name: PurgeMessagesByID :execrows
UPDATE chat.messages
SET body = NULL, purged_at = @now, updated_at = @now
WHERE id = ANY(@ids::uuid[]) AND NOT legal_hold AND purged_at IS NULL;

-- name: DeleteMemberReports :execrows
DELETE FROM chat.reports WHERE reporter_id = @member_id;

-- name: DeleteMemberRoomMemberships :execrows
DELETE FROM chat.room_members WHERE member_id = @member_id;

-- name: DeleteMemberRestrictions :execrows
DELETE FROM chat.room_restrictions WHERE member_id = @member_id;

-- name: DeleteMemberMentorships :execrows
DELETE FROM chat.room_mentors WHERE member_id = @member_id;

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

-- name: RemoveMemberMentorships :many
-- Ends every active assignment of the member (mentor role taken in
-- membership). Rows stay with removed_at set; already removed rows are
-- untouched, so a replay returns nothing.
UPDATE chat.room_mentors
SET removed_at = @removed_at
WHERE member_id = @member_id AND removed_at IS NULL
RETURNING room_id;

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
