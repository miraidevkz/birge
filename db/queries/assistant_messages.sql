-- Account deletion: her whole dialogue, before the retention period ends.
-- name: DeleteMemberMessages :execrows
DELETE FROM assistant.messages WHERE member_id = @member_id;

-- name: InsertMemberTurn :one
INSERT INTO assistant.messages (
    id, member_id, role, text_sealed, crisis_flag, idempotency_key, created_at
) VALUES (
    @id, @member_id, 'member', @text_sealed, @crisis_flag, @idempotency_key, @created_at
)
ON CONFLICT (member_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
RETURNING id;

-- name: InsertAssistantTurn :exec
INSERT INTO assistant.messages (
    id, member_id, role, reply_to, text_sealed, crisis_flag, degraded, sources, suggest, escalation_id, created_at
) VALUES (
    @id, @member_id, 'assistant', @reply_to, @text_sealed, @crisis_flag, @degraded, @sources, @suggest, @escalation_id, @created_at
);

-- name: GetMemberTurnByIdempotencyKey :one
SELECT id, member_id, role, reply_to, text_sealed, crisis_flag, degraded, sources, suggest,
       escalation_id, idempotency_key, helped, rated_at, created_at
FROM assistant.messages
WHERE member_id = @member_id AND idempotency_key = @idempotency_key;

-- name: GetReplyTo :one
SELECT id, member_id, role, reply_to, text_sealed, crisis_flag, degraded, sources, suggest,
       escalation_id, idempotency_key, helped, rated_at, created_at
FROM assistant.messages
WHERE reply_to = @reply_to;

-- name: CountMemberTurnsSince :one
SELECT count(*)::integer AS n
FROM assistant.messages
WHERE member_id = @member_id AND role = 'member' AND created_at >= @since;

-- name: ListRecentTurns :many
SELECT id, member_id, role, reply_to, text_sealed, crisis_flag, degraded, sources, suggest,
       escalation_id, idempotency_key, helped, rated_at, created_at
FROM assistant.messages
WHERE member_id = @member_id
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: GetAssistantTurnForUpdate :one
SELECT id, helped
FROM assistant.messages
WHERE id = @id AND member_id = @member_id AND role = 'assistant'
FOR UPDATE;

-- name: RateAssistantTurn :exec
UPDATE assistant.messages
SET helped = @helped, rated_at = @rated_at
WHERE id = @id AND member_id = @member_id AND role = 'assistant';

-- name: PurgeTurnsBefore :execrows
-- Retention (90 days, docs/requirements/mvp.md § 7): one bounded batch per call.
DELETE FROM assistant.messages
WHERE id IN (
    SELECT old.id FROM assistant.messages AS old
    WHERE old.created_at < sqlc.arg(cutoff)::timestamptz
    ORDER BY old.created_at
    LIMIT sqlc.arg(lim)::integer
);
