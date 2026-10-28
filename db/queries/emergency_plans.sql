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

-- name: InsertPlanLink :exec
INSERT INTO emergency.plan_links (id, token_hash, contact_id, owner_id, sent_at, created_at, expires_at)
VALUES (@id, @token_hash, @contact_id, @owner_id, @sent_at, @created_at, @expires_at);

-- name: RevokeContactPlanLinks :execrows
UPDATE emergency.plan_links
SET revoked_at = @revoked_at
WHERE contact_id = @contact_id
  AND revoked_at IS NULL;

-- name: RevokeOwnerPlanLinks :execrows
UPDATE emergency.plan_links
SET revoked_at = @revoked_at
WHERE owner_id = @owner_id
  AND revoked_at IS NULL;

-- name: GetLivePlanLink :one
-- One query for every failure (unknown, revoked, expired), so they cannot be
-- told apart by status or by the number of round trips.
SELECT id, token_hash, contact_id, owner_id, sent_at, created_at, expires_at, revoked_at
FROM emergency.plan_links
WHERE token_hash = @token_hash
  AND revoked_at IS NULL
  AND expires_at > @now;

-- name: DeleteExpiredPlanLink :execrows
-- Retention (emergency.PlanLinkPurge): only a link that expired before cutoff.
DELETE FROM emergency.plan_links
WHERE id = @id
  AND expires_at <= @cutoff;

-- name: ListOwnerPlanLinkIDs :many
SELECT id FROM emergency.plan_links WHERE owner_id = @owner_id;

-- name: DeleteOwnerPlanLinks :execrows
DELETE FROM emergency.plan_links WHERE owner_id = @owner_id;
