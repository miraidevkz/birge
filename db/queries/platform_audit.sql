-- name: InsertAuditEntry :exec
INSERT INTO platform.audit_log (actor_id, actor_role, action, object_type, object_id, reason)
VALUES (@actor_id, @actor_role, @action, @object_type, @object_id, @reason);
