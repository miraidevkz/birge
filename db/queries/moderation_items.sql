-- name: InsertItem :one
-- Creates the open item of an object, or returns nothing when one is already
-- open (a redelivered or repeated submission).
INSERT INTO moderation.items (id, kind, object_id, status, source_reason, created_at)
VALUES (@id, @kind, @object_id, 'open', sqlc.narg(source_reason), @created_at)
ON CONFLICT (kind, object_id) WHERE status = 'open' DO NOTHING
RETURNING id;

-- name: UpsertReportItem :one
-- A report on a message that already has an open report item bumps its
-- counter instead of creating a second item.
INSERT INTO moderation.items (id, kind, object_id, status, source_reason, created_at)
VALUES (@id, 'report', @object_id, 'open', sqlc.narg(source_reason), @created_at)
ON CONFLICT (kind, object_id) WHERE status = 'open'
DO UPDATE SET report_count = moderation.items.report_count + 1
RETURNING id, report_count;

-- name: GetItemForUpdate :one
-- Row lock: concurrent decisions on one item are serialised here.
SELECT id, kind, object_id, status, source_reason, report_count, created_at,
       decided_by, decided_at, approve, reason, block_author, legal_hold
FROM moderation.items
WHERE id = @id
FOR UPDATE;

-- name: DecideItem :one
UPDATE moderation.items
SET status = 'decided',
    decided_by = @decided_by,
    decided_at = @decided_at,
    approve = @approve,
    reason = sqlc.narg(reason),
    block_author = @block_author,
    legal_hold = @legal_hold
WHERE id = @id
  AND status = 'open'
RETURNING id;

-- name: ListOpenItems :many
-- Oldest first. The cursor is the last row of the previous page.
SELECT id, kind, object_id, status, source_reason, report_count, created_at,
       decided_by, decided_at, approve, reason, block_author, legal_hold
FROM moderation.items
WHERE status = 'open'
  AND (sqlc.narg(filter_kind)::text IS NULL OR kind = sqlc.narg(filter_kind))
  AND (
    sqlc.narg(cursor_created_at)::timestamptz IS NULL
    OR (created_at, id) > (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid)
  )
ORDER BY created_at, id
LIMIT @page_limit;
