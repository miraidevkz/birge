-- name: InsertTrustedContact :one
-- Does nothing (no row) on a repeated Idempotency-Key or when the member is
-- already an active contact of the owner.
INSERT INTO emergency.trusted_contacts
    (id, owner_id, idempotency_key, kind, contact_member_id, name, phone, lang, status, created_at, confirmed_at)
VALUES (@id, @owner_id, @idempotency_key, @kind, sqlc.narg('contact_member_id')::uuid, sqlc.narg('name')::bytea,
        sqlc.narg('phone')::bytea, @lang, @status, @created_at, sqlc.narg('confirmed_at')::timestamptz)
ON CONFLICT DO NOTHING
RETURNING id, owner_id, idempotency_key, kind, contact_member_id, name, phone, lang, status, created_at, confirmed_at, plan_sent_at, removed_at;

-- name: GetTrustedContactByIdempotencyKey :one
SELECT id, owner_id, idempotency_key, kind, contact_member_id, name, phone, lang, status, created_at, confirmed_at, plan_sent_at, removed_at
FROM emergency.trusted_contacts
WHERE owner_id = @owner_id
  AND idempotency_key = @idempotency_key;

-- name: GetTrustedContact :one
SELECT id, owner_id, idempotency_key, kind, contact_member_id, name, phone, lang, status, created_at, confirmed_at, plan_sent_at, removed_at
FROM emergency.trusted_contacts
WHERE id = @id;

-- name: ListActiveTrustedContacts :many
SELECT id, owner_id, idempotency_key, kind, contact_member_id, name, phone, lang, status, created_at, confirmed_at, plan_sent_at, removed_at
FROM emergency.trusted_contacts
WHERE owner_id = @owner_id
  AND removed_at IS NULL
ORDER BY created_at ASC, id ASC;

-- name: ConfirmTrustedContact :one
-- Only the invited member, only while the invitation is active.
UPDATE emergency.trusted_contacts
SET status = 'confirmed', confirmed_at = @confirmed_at
WHERE id = @id
  AND kind = 'member'
  AND contact_member_id = @member_id
  AND status = 'invited'
  AND removed_at IS NULL
RETURNING id, owner_id, idempotency_key, kind, contact_member_id, name, phone, lang, status, created_at, confirmed_at, plan_sent_at, removed_at;

-- name: RemoveTrustedContact :execrows
UPDATE emergency.trusted_contacts
SET removed_at = @removed_at
WHERE id = @id
  AND owner_id = @owner_id
  AND removed_at IS NULL;

-- name: MarkContactsPlanSent :execrows
UPDATE emergency.trusted_contacts
SET plan_sent_at = @sent_at
WHERE owner_id = @owner_id
  AND status = 'confirmed'
  AND removed_at IS NULL;

-- name: GetSharedPlanContact :one
-- The active, confirmed member contact through which member_id may read the
-- owner's plan.
SELECT id, owner_id, idempotency_key, kind, contact_member_id, name, phone, lang, status, created_at, confirmed_at, plan_sent_at, removed_at
FROM emergency.trusted_contacts
WHERE owner_id = @owner_id
  AND contact_member_id = @member_id
  AND kind = 'member'
  AND status = 'confirmed'
  AND removed_at IS NULL;
