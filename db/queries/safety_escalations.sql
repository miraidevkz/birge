-- name: InsertEscalation :one
-- Does nothing (no row) when the member already has an open or taken escalation.
INSERT INTO safety.escalations (member_id, source, status, district_id, opened_at)
VALUES (@member_id, @source, 'open', sqlc.narg('district_id')::uuid, @opened_at)
ON CONFLICT (member_id) WHERE status IN ('open', 'taken') DO NOTHING
RETURNING id, member_id, source, status, district_id, opened_at, taken_by, taken_at, outcome, note, closed_at;

-- name: GetActiveEscalationByMember :one
SELECT id, member_id, source, status, district_id, opened_at, taken_by, taken_at, outcome, note, closed_at
FROM safety.escalations
WHERE member_id = @member_id
  AND status IN ('open', 'taken');

-- name: GetEscalation :one
SELECT id, member_id, source, status, district_id, opened_at, taken_by, taken_at, outcome, note, closed_at
FROM safety.escalations
WHERE id = @id;

-- name: GetEscalationForUpdate :one
SELECT id, member_id, source, status, district_id, opened_at, taken_by, taken_at, outcome, note, closed_at
FROM safety.escalations
WHERE id = @id
FOR UPDATE;

-- name: TakeEscalation :one
UPDATE safety.escalations
SET status = 'taken', taken_by = @taken_by, taken_at = @taken_at
WHERE id = @id
  AND status = 'open'
RETURNING id, member_id, source, status, district_id, opened_at, taken_by, taken_at, outcome, note, closed_at;

-- name: CloseEscalation :one
UPDATE safety.escalations
SET status = 'closed', outcome = @outcome, note = sqlc.narg('note')::bytea, closed_at = @closed_at
WHERE id = @id
  AND status = 'taken'
  AND taken_by = @taken_by
RETURNING id, member_id, source, status, district_id, opened_at, taken_by, taken_at, outcome, note, closed_at;

-- Account deletion: the escalation stays as a shell for staff accountability
-- (IDs, source, status, times, outcome); the note and the district go.
-- name: ClearMemberEscalations :execrows
UPDATE safety.escalations
SET note = NULL, district_id = NULL
WHERE member_id = @member_id
  AND (note IS NOT NULL OR district_id IS NOT NULL);

-- name: ListEscalations :many
SELECT id, member_id, source, status, district_id, opened_at, taken_by, taken_at, outcome, note, closed_at
FROM safety.escalations
WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (
    sqlc.narg('cursor_opened_at')::timestamptz IS NULL
    OR (opened_at, id) < (sqlc.narg('cursor_opened_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY opened_at DESC, id DESC
LIMIT @page_limit;
