-- name: InsertMessage :one
-- Does nothing (no row) when the author already used this client id.
INSERT INTO chat.messages (id, room_id, seq, author_id, client_id, body, status, hold_reason, created_at, updated_at)
VALUES (@id, @room_id, @seq, @author_id, @client_id, @body, @status, @hold_reason, @created_at, @created_at)
ON CONFLICT (author_id, client_id) DO NOTHING
RETURNING id, room_id, seq, author_id, client_id, body, status, hold_reason, escalation_id, legal_hold, created_at, updated_at, purged_at;

-- name: GetMessageByClientID :one
SELECT id, room_id, seq, author_id, client_id, body, status, hold_reason, escalation_id, legal_hold, created_at, updated_at, purged_at
FROM chat.messages
WHERE author_id = @author_id AND client_id = @client_id;

-- name: GetMessage :one
SELECT id, room_id, seq, author_id, client_id, body, status, hold_reason, escalation_id, legal_hold, created_at, updated_at, purged_at
FROM chat.messages
WHERE id = @id;

-- name: GetMessageForUpdate :one
SELECT id, room_id, seq, author_id, client_id, body, status, hold_reason, escalation_id, legal_hold, created_at, updated_at, purged_at
FROM chat.messages
WHERE id = @id
FOR UPDATE;

-- name: ListMessagesAfter :many
SELECT id, room_id, seq, author_id, client_id, body, status, hold_reason, escalation_id, legal_hold, created_at, updated_at, purged_at
FROM chat.messages
WHERE room_id = @room_id AND seq > @after_seq
ORDER BY seq
LIMIT @page_limit;

-- name: ListMessagesBefore :many
-- Newest first; the service reverses the page.
SELECT id, room_id, seq, author_id, client_id, body, status, hold_reason, escalation_id, legal_hold, created_at, updated_at, purged_at
FROM chat.messages
WHERE room_id = @room_id AND seq < @before_seq
ORDER BY seq DESC
LIMIT @page_limit;

-- name: CountAuthorMessagesSince :one
SELECT count(*)::integer
FROM chat.messages
WHERE author_id = @author_id AND created_at > @since;

-- name: CountAuthorGroupMessages :one
-- Newcomer hold: how many messages the author has posted in group rooms,
-- counted up to the newcomer limit.
SELECT count(*)::integer
FROM (
    SELECT 1
    FROM chat.messages m
    JOIN chat.rooms r ON r.id = m.room_id
    WHERE m.author_id = @author_id AND r.kind <> 'direct'
    LIMIT @up_to
) AS counted;

-- name: LastAuthorMessageAt :one
SELECT created_at
FROM chat.messages
WHERE room_id = @room_id AND author_id = @author_id
ORDER BY created_at DESC
LIMIT 1;

-- name: SetMessageStatus :one
UPDATE chat.messages
SET status = @status, updated_at = @updated_at
WHERE id = @id
RETURNING id, room_id, seq, author_id, client_id, body, status, hold_reason, escalation_id, legal_hold, created_at, updated_at, purged_at;

-- name: SetMessageEscalation :execrows
UPDATE chat.messages
SET escalation_id = @escalation_id
WHERE id = @id AND escalation_id IS NULL;

-- name: SetMessageLegalHold :execrows
UPDATE chat.messages
SET legal_hold = @legal_hold
WHERE id = @id;

-- name: PurgeExpiredMessages :execrows
-- Retention: clears the text of messages older than created_before in rooms
-- of the given kinds, except under legal hold or an open report. The row
-- stays as a "deleted" tombstone; at most page_limit rows per call.
UPDATE chat.messages
SET body = NULL, purged_at = @purged_at, updated_at = @purged_at
WHERE id IN (
    SELECT m.id
    FROM chat.messages m
    JOIN chat.rooms r ON r.id = m.room_id
    WHERE m.purged_at IS NULL
      AND m.created_at < @created_before
      AND r.kind = ANY (@kinds::text[])
      AND NOT m.legal_hold
      AND NOT EXISTS (
          SELECT 1 FROM chat.reports rp WHERE rp.message_id = m.id AND rp.closed_at IS NULL
      )
    ORDER BY m.created_at
    LIMIT @page_limit
    FOR UPDATE OF m SKIP LOCKED
);
