-- name: InsertNotification :one
INSERT INTO notify.notifications (request_id, recipient_kind, recipient_ref, template, params, class, created_at)
VALUES (@request_id, @recipient_kind, @recipient_ref, @template, @params, @class, @created_at)
ON CONFLICT (request_id, recipient_kind, recipient_ref) DO NOTHING
RETURNING id, request_id, recipient_kind, recipient_ref, template, params, class, created_at, read_at, acknowledged_at;

-- name: GetNotificationByRequestRecipient :one
SELECT id, request_id, recipient_kind, recipient_ref, template, params, class, created_at, read_at, acknowledged_at
FROM notify.notifications
WHERE request_id = @request_id
  AND recipient_kind = @recipient_kind
  AND recipient_ref = @recipient_ref;

-- name: GetNotification :one
SELECT id, request_id, recipient_kind, recipient_ref, template, params, class, created_at, read_at, acknowledged_at
FROM notify.notifications
WHERE id = @id;

-- name: ListInbox :many
SELECT id, request_id, recipient_kind, recipient_ref, template, params, class, created_at, read_at, acknowledged_at
FROM notify.notifications
WHERE recipient_kind = 'member'
  AND recipient_ref = @recipient_ref
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT @page_limit;

-- name: MarkNotificationRead :one
UPDATE notify.notifications
SET
    read_at = COALESCE(read_at, @read_at),
    acknowledged_at = CASE
        WHEN class = 'critical' THEN COALESCE(acknowledged_at, @read_at)
        ELSE acknowledged_at
    END
WHERE id = @id
  AND recipient_kind = 'member'
  AND recipient_ref = @recipient_ref
RETURNING id, request_id, recipient_kind, recipient_ref, template, params, class, created_at, read_at, acknowledged_at;

-- name: AcknowledgeNotification :execrows
UPDATE notify.notifications
SET acknowledged_at = COALESCE(acknowledged_at, @acknowledged_at)
WHERE id = @id AND acknowledged_at IS NULL;
