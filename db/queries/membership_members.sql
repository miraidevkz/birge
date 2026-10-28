-- The tombstone of a deleted member is found by identity (deleted_at set),
-- so the identity is refused instead of getting a new member.
-- name: GetMemberByIdentity :one
SELECT m.id, m.identity_id, m.display_name, m.lang, m.district_id, d.city_id, m.verification, m.verified_at, m.roles,
       m.suspended_at, m.suspended_reason, m.deleted_at
FROM membership.members m
LEFT JOIN membership.districts d ON d.id = m.district_id
WHERE m.identity_id = @identity_id;

-- A deleted member's tombstone is not found by ID: for every other context
-- she no longer exists (shown as deleted).
-- name: GetMemberByID :one
SELECT m.id, m.identity_id, m.display_name, m.lang, m.district_id, d.city_id, m.verification, m.verified_at, m.roles,
       m.suspended_at, m.suspended_reason, m.deleted_at
FROM membership.members m
LEFT JOIN membership.districts d ON d.id = m.district_id
WHERE m.id = @id AND m.deleted_at IS NULL;

-- name: LockMember :one
SELECT id FROM membership.members
WHERE id = @id
FOR UPDATE;

-- InsertMember returns no row when the identity already has a member, so
-- concurrent first requests create exactly one.
-- name: InsertMember :one
INSERT INTO membership.members (identity_id, created_at, updated_at)
VALUES (@identity_id, @now, @now)
ON CONFLICT (identity_id) DO NOTHING
RETURNING id;

-- name: UpdateMemberProfile :execrows
UPDATE membership.members
SET display_name = coalesce(sqlc.narg(display_name)::bytea, display_name),
    lang = coalesce(sqlc.narg(lang)::text, lang),
    district_id = coalesce(sqlc.narg(district_id)::uuid, district_id),
    updated_at = @now
WHERE id = @id AND deleted_at IS NULL;

-- name: SetMemberVerification :execrows
UPDATE membership.members
SET verification = @verification,
    verified_at = CASE WHEN @verification = 'verified' THEN coalesce(verified_at, @now) ELSE NULL END,
    updated_at = @now
WHERE id = @id AND deleted_at IS NULL;

-- A deleted member's tombstone is never suspended (zero rows: the caller
-- treats it as nothing to do).
-- name: SetMemberSuspension :execrows
UPDATE membership.members
SET suspended_at = @suspended_at,
    suspended_reason = @suspended_reason,
    updated_at = @now
WHERE id = @id AND deleted_at IS NULL;

-- Verified, not suspended and not deleted: a suspended member is hidden like
-- a block (ADR-0019).
-- name: ListVerifiedInDistrict :many
SELECT id
FROM membership.members
WHERE district_id = @district_id AND verification = 'verified' AND suspended_at IS NULL AND deleted_at IS NULL
ORDER BY id;

-- Account deletion (privacy.DataDeletionRequested). The member row stays as
-- a tombstone that keeps only id and identity_id, so the identity can never
-- create a new member (lazy creation) and a replayed old session is refused.

-- name: LockMemberForDeletion :one
SELECT identity_id, deleted_at IS NOT NULL AS deleted
FROM membership.members
WHERE id = @id
FOR UPDATE;

-- The suspension goes too (both columns, schema CHECK): the tombstone is
-- refused as deleted, not as suspended.
-- name: TombstoneMember :execrows
UPDATE membership.members
SET display_name = NULL, district_id = NULL, verification = 'new', roles = '{}',
    verified_at = NULL,
    suspended_at = NULL, suspended_reason = NULL,
    updated_at = @now, deleted_at = @now
WHERE id = @id AND deleted_at IS NULL;

-- name: DeleteMemberChildren :execrows
DELETE FROM membership.children WHERE member_id = @member_id;

-- name: DeleteMemberVerificationRequests :execrows
DELETE FROM membership.verification_requests WHERE member_id = @member_id;

-- Staff member list: verified members (most recently verified first).
-- name: ListStaffMembersVerified :many
SELECT id, display_name, district_id, verification, verified_at,
       suspended_at
FROM membership.members
WHERE deleted_at IS NULL
  AND verification = 'verified'
  AND (sqlc.narg(district_id)::uuid IS NULL OR district_id = sqlc.narg(district_id))
  AND (
    sqlc.narg(cursor_verified_at)::timestamptz IS NULL
    OR (verified_at, id) < (sqlc.narg(cursor_verified_at)::timestamptz, sqlc.narg(cursor_id)::uuid)
  )
ORDER BY verified_at DESC, id DESC
LIMIT @page_limit;

-- Staff member list: suspended members (most recently suspended first).
-- name: ListStaffMembersSuspended :many
SELECT id, display_name, district_id, verification, verified_at,
       suspended_at
FROM membership.members
WHERE deleted_at IS NULL
  AND suspended_at IS NOT NULL
  AND (sqlc.narg(district_id)::uuid IS NULL OR district_id = sqlc.narg(district_id))
  AND (
    sqlc.narg(cursor_suspended_at)::timestamptz IS NULL
    OR (suspended_at, id) < (sqlc.narg(cursor_suspended_at)::timestamptz, sqlc.narg(cursor_id)::uuid)
  )
ORDER BY suspended_at DESC, id DESC
LIMIT @page_limit;
