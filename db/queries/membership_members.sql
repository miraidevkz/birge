-- name: GetMemberByIdentity :one
SELECT m.id, m.identity_id, m.display_name, m.lang, m.district_id, d.city_id, m.verification, m.roles,
       m.suspended_at, m.suspended_reason
FROM membership.members m
LEFT JOIN membership.districts d ON d.id = m.district_id
WHERE m.identity_id = @identity_id;

-- name: GetMemberByID :one
SELECT m.id, m.identity_id, m.display_name, m.lang, m.district_id, d.city_id, m.verification, m.roles,
       m.suspended_at, m.suspended_reason
FROM membership.members m
LEFT JOIN membership.districts d ON d.id = m.district_id
WHERE m.id = @id;

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
WHERE id = @id;

-- name: SetMemberVerification :execrows
UPDATE membership.members
SET verification = @verification, updated_at = @now
WHERE id = @id;

-- name: SetMemberSuspension :execrows
UPDATE membership.members
SET suspended_at = @suspended_at,
    suspended_reason = @suspended_reason,
    updated_at = @now
WHERE id = @id;

-- Verified and not suspended: a suspended member is hidden like a block (ADR-0019).
-- name: ListVerifiedInDistrict :many
SELECT id
FROM membership.members
WHERE district_id = @district_id AND verification = 'verified' AND suspended_at IS NULL
ORDER BY id;
