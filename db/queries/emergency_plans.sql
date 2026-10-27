-- name: UpsertChildCard :exec
INSERT INTO emergency.child_cards (member_id, content, updated_at)
VALUES (@member_id, @content, @updated_at)
ON CONFLICT (member_id) DO UPDATE
SET content = EXCLUDED.content, updated_at = EXCLUDED.updated_at;

-- name: GetChildCard :one
SELECT member_id, content, updated_at, last_sent_at
FROM emergency.child_cards
WHERE member_id = @member_id;

-- name: MarkChildCardSent :exec
UPDATE emergency.child_cards
SET last_sent_at = @sent_at
WHERE member_id = @member_id;

-- name: InsertPlanSend :one
-- Does nothing (no row) when the member already used this Idempotency-Key.
INSERT INTO emergency.plan_sends (id, member_id, idempotency_key, contact_count, sent_at)
VALUES (@id, @member_id, @idempotency_key, @contact_count, @sent_at)
ON CONFLICT (member_id, idempotency_key) DO NOTHING
RETURNING id, member_id, idempotency_key, contact_count, sent_at;

-- name: GetPlanSendByIdempotencyKey :one
SELECT id, member_id, idempotency_key, contact_count, sent_at
FROM emergency.plan_sends
WHERE member_id = @member_id
  AND idempotency_key = @idempotency_key;

-- name: CountPlanSendsSince :one
SELECT count(*)::integer
FROM emergency.plan_sends
WHERE member_id = @member_id
  AND sent_at > @since;
