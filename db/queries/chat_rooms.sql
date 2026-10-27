-- name: ChatAdvisoryXactLock :exec
-- Serialises a member's sends (rate limit, idempotent replay) until the
-- transaction ends. Always taken before a room row lock, never after.
SELECT pg_advisory_xact_lock(hashtextextended(@scope_key::text, 0));

-- name: InsertGroupRoom :one
-- Does nothing (no row) when the district or topic already has a room.
INSERT INTO chat.rooms (id, kind, district_id, topic_code, title_key, created_at)
VALUES (@id, @kind, @district_id, @topic_code, @title_key, @created_at)
ON CONFLICT DO NOTHING
RETURNING id, kind, district_id, topic_code, title_key, origin, origin_id, member_low, member_high, slow_mode_seconds, last_seq, created_at;

-- name: GetDistrictRoom :one
SELECT id, kind, district_id, topic_code, title_key, origin, origin_id, member_low, member_high, slow_mode_seconds, last_seq, created_at
FROM chat.rooms
WHERE kind = 'district' AND district_id = @district_id;

-- name: GetTopicRoom :one
SELECT id, kind, district_id, topic_code, title_key, origin, origin_id, member_low, member_high, slow_mode_seconds, last_seq, created_at
FROM chat.rooms
WHERE kind = 'topic' AND topic_code = @topic_code;

-- name: InsertDirectRoom :one
-- Does nothing (no row) when this origin already opened a room for the pair.
INSERT INTO chat.rooms (id, kind, origin, origin_id, member_low, member_high, created_at)
VALUES (@id, 'direct', @origin, @origin_id, @member_low, @member_high, @created_at)
ON CONFLICT (origin, origin_id, member_low, member_high) WHERE kind = 'direct' DO NOTHING
RETURNING id, kind, district_id, topic_code, title_key, origin, origin_id, member_low, member_high, slow_mode_seconds, last_seq, created_at;

-- name: GetDirectRoom :one
SELECT id, kind, district_id, topic_code, title_key, origin, origin_id, member_low, member_high, slow_mode_seconds, last_seq, created_at
FROM chat.rooms
WHERE kind = 'direct' AND origin = @origin AND origin_id = @origin_id
  AND member_low = @member_low AND member_high = @member_high;

-- name: GetRoom :one
SELECT id, kind, district_id, topic_code, title_key, origin, origin_id, member_low, member_high, slow_mode_seconds, last_seq, created_at
FROM chat.rooms
WHERE id = @id;

-- name: GetRoomForUpdate :one
SELECT id, kind, district_id, topic_code, title_key, origin, origin_id, member_low, member_high, slow_mode_seconds, last_seq, created_at
FROM chat.rooms
WHERE id = @id
FOR UPDATE;

-- name: NextRoomSeq :one
-- Takes the room row lock until the transaction ends; a rollback returns
-- the number, so committed sequences have no gaps.
UPDATE chat.rooms
SET last_seq = last_seq + 1
WHERE id = @id
RETURNING last_seq;

-- name: SetRoomSlowMode :execrows
UPDATE chat.rooms
SET slow_mode_seconds = @slow_mode_seconds
WHERE id = @id;

-- name: ListJoinableGroupRooms :many
-- Group rooms a verified member may join: her district's room and every
-- topic room.
SELECT id, kind, district_id, topic_code, title_key, origin, origin_id, member_low, member_high, slow_mode_seconds, last_seq, created_at
FROM chat.rooms
WHERE (kind = 'district' AND district_id = sqlc.narg('district_id')::uuid)
   OR kind = 'topic'
ORDER BY kind, created_at, id
LIMIT 200;

-- name: ListMemberRooms :many
-- Rooms the member is in, with her mute flag.
SELECT r.id, r.kind, r.district_id, r.topic_code, r.title_key, r.origin, r.origin_id, r.member_low, r.member_high,
       r.slow_mode_seconds, r.last_seq, r.created_at, m.muted
FROM chat.rooms r
JOIN chat.room_members m ON m.room_id = r.id
WHERE m.member_id = @member_id AND m.left_at IS NULL
ORDER BY r.kind, r.created_at, r.id
LIMIT 200;

-- name: GetRoomMember :one
SELECT room_id, member_id, joined_at, left_at, muted, last_push_at, connected_until
FROM chat.room_members
WHERE room_id = @room_id AND member_id = @member_id;

-- name: JoinRoom :exec
-- Joins or re-joins; keeps the mute flag of an earlier membership.
INSERT INTO chat.room_members (room_id, member_id, joined_at)
VALUES (@room_id, @member_id, @joined_at)
ON CONFLICT (room_id, member_id) DO UPDATE
SET left_at = NULL,
    joined_at = CASE WHEN chat.room_members.left_at IS NULL THEN chat.room_members.joined_at ELSE EXCLUDED.joined_at END;

-- name: AddRoomMemberIfAbsent :exec
-- Adds a direct room's member once; never brings back one who left.
INSERT INTO chat.room_members (room_id, member_id, joined_at)
VALUES (@room_id, @member_id, @joined_at)
ON CONFLICT (room_id, member_id) DO NOTHING;

-- name: LeaveRoom :execrows
UPDATE chat.room_members
SET left_at = @left_at
WHERE room_id = @room_id AND member_id = @member_id AND left_at IS NULL;

-- name: SetRoomMute :execrows
UPDATE chat.room_members
SET muted = @muted
WHERE room_id = @room_id AND member_id = @member_id AND left_at IS NULL;

-- name: TouchPresence :execrows
UPDATE chat.room_members
SET connected_until = @connected_until
WHERE room_id = @room_id AND member_id = @member_id AND left_at IS NULL;

-- name: ListPushCandidates :many
-- Members to push for a new message: in the room, not the author, not
-- muted, not connected to the room, not pushed for it since pushed_before.
SELECT member_id
FROM chat.room_members
WHERE room_id = @room_id
  AND left_at IS NULL
  AND member_id <> @author_id
  AND NOT muted
  AND (connected_until IS NULL OR connected_until <= @now)
  AND (last_push_at IS NULL OR last_push_at <= @pushed_before)
ORDER BY member_id
LIMIT 5000;

-- name: MarkPushed :exec
UPDATE chat.room_members
SET last_push_at = @pushed_at
WHERE room_id = @room_id AND member_id = @member_id;
