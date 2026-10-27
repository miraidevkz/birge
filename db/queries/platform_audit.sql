-- name: InsertAuditEntry :exec
INSERT INTO platform.audit_log (actor_id, actor_role, action, object_type, object_id, reason)
VALUES (@actor_id, @actor_role, @action, @object_type, @object_id, @reason);

-- name: AuditEntryExists :one
SELECT EXISTS (
    SELECT 1 FROM platform.audit_log
    WHERE object_type = @object_type AND object_id = @object_id::uuid AND action = @action
) AS found;
