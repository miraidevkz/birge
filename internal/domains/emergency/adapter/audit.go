package adapter

import (
	"context"
	"fmt"

	"birge/internal/domains/emergency/model"
)

// insertAuditSQL appends one platform.audit_log row.
//
// TEMPORARY, as in safety: platform.audit_log belongs to platform, which has
// no audit API yet, and a context may not put platform queries in its own
// sqlc files. This raw statement is the only place emergency touches the
// table; replace it with the platform audit package when it exists (README,
// Open questions).
const insertAuditSQL = `INSERT INTO platform.audit_log
    (occurred_at, actor_id, actor_role, action, object_type, object_id, reason)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

// Audit implements port.Auditor in the current transaction, so the entry
// commits or rolls back with the read or change it records.
func (t *postgresTx) Audit(ctx context.Context, e model.AuditEntry) error {
	var reason *string
	if e.Reason != "" {
		reason = &e.Reason
	}
	if _, err := t.tx.Exec(ctx, insertAuditSQL,
		e.At, e.ActorID, e.ActorRole, e.Action, e.ObjectType, e.ObjectID, reason,
	); err != nil {
		return fmt.Errorf("audit %s: %w", e.Action, err)
	}
	return nil
}
