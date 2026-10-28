-- name: InsertAuditEntry :exec
INSERT INTO platform.audit_log (actor_id, actor_role, action, object_type, object_id, reason)
VALUES (@actor_id, @actor_role, @action, @object_type, @object_id, @reason);

-- name: AuditEntryExists :one
SELECT EXISTS (
    SELECT 1 FROM platform.audit_log
    WHERE object_type = @object_type AND object_id = @object_id::uuid AND action = @action
) AS found;

-- name: RecentAuditObjects :many
-- The objects among object_ids that actor already has an entry with action
-- for in the last within_seconds, measured on the database clock (the one
-- that set occurred_at). One indexed scan per page (audit_log_object_idx).
SELECT DISTINCT object_id::uuid AS object_id
FROM platform.audit_log
WHERE object_type = @object_type
  AND object_id = ANY(@object_ids::uuid[])
  AND occurred_at > now() - make_interval(secs => @within_seconds::double precision)
  AND actor_id = @actor_id
  AND action = @action;
