-- Recorded by the staff auth middleware at most hourly per staff ID (package
-- staffdir). Name and roles become what the IdP sent last; first_seen_at is
-- set once. A write older than the stored last_seen_at (another replica, out
-- of order) changes nothing, so a stale name or role set never wins.
-- name: UpsertStaffAccount :exec
INSERT INTO platform.staff_accounts (staff_id, display_name, roles, first_seen_at, last_seen_at)
VALUES (@staff_id, sqlc.narg(display_name)::text, @roles::text[], @seen_at, @seen_at)
ON CONFLICT (staff_id) DO UPDATE
SET display_name = EXCLUDED.display_name,
    roles = EXCLUDED.roles,
    last_seen_at = EXCLUDED.last_seen_at
WHERE platform.staff_accounts.last_seen_at <= EXCLUDED.last_seen_at;

-- name: ListStaffAccounts :many
SELECT staff_id, display_name, roles, first_seen_at, last_seen_at
FROM platform.staff_accounts
ORDER BY last_seen_at DESC, staff_id
LIMIT @row_limit;

-- name: GetStaffAccount :one
SELECT staff_id, display_name, roles, first_seen_at, last_seen_at
FROM platform.staff_accounts
WHERE staff_id = @staff_id;

-- Entries by staff only (actor_role one of @staff_roles), newest first, with
-- keyset paging on (occurred_at, id). Every filter is optional.
-- name: ListStaffAuditEntries :many
SELECT a.id, a.occurred_at, a.actor_id, a.actor_role, a.action, a.object_type, a.object_id, a.reason,
       s.display_name AS actor_name
FROM platform.audit_log a
LEFT JOIN platform.staff_accounts s ON s.staff_id = a.actor_id
WHERE a.actor_role = ANY (@staff_roles::text[])
  AND (sqlc.narg(actor_id)::uuid IS NULL OR a.actor_id = sqlc.narg(actor_id)::uuid)
  AND (sqlc.narg(object_type)::text IS NULL OR a.object_type = sqlc.narg(object_type)::text)
  AND (sqlc.narg(object_id)::uuid IS NULL OR a.object_id = sqlc.narg(object_id)::uuid)
  AND (sqlc.narg(action)::text IS NULL OR a.action = sqlc.narg(action)::text)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR a.occurred_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR a.occurred_at <= sqlc.narg(to_at)::timestamptz)
  AND (
    sqlc.narg(cursor_at)::timestamptz IS NULL
    OR (a.occurred_at, a.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid)
  )
ORDER BY a.occurred_at DESC, a.id DESC
LIMIT @page_limit;

-- Count per action of one staff member since @since (staff roles only).
-- name: CountStaffActionsSince :many
SELECT action, count(*)::integer AS n
FROM platform.audit_log
WHERE actor_id = @actor_id
  AND occurred_at >= @since
  AND actor_role = ANY (@staff_roles::text[])
GROUP BY action
ORDER BY n DESC, action
LIMIT @row_limit;
