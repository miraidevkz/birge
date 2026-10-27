package adapter

import (
	"context"

	"birge/internal/domains/safety/model"
	"birge/internal/platform/audit"
)

// Audit implements port.Auditor in the current transaction, so the entry
// commits or rolls back with the change it records.
func (t *postgresTx) Audit(ctx context.Context, e model.AuditEntry) error {
	return audit.Record(ctx, t.platform, audit.Entry{
		ActorID:    e.ActorID,
		ActorRole:  e.ActorRole,
		Action:     e.Action,
		ObjectType: e.ObjectType,
		ObjectID:   e.ObjectID,
		Reason:     e.Reason,
	})
}
