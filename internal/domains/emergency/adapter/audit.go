package adapter

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/platform/audit"
)

// Audit implements port.Auditor in the current transaction, so the entry
// commits or rolls back with the read or change it records.
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

// RecentStaffSosViews implements port.Tx: one query over platform.audit_log
// for the whole page, on the database clock that set occurred_at.
func (t *postgresTx) RecentStaffSosViews(ctx context.Context, staffID uuid.UUID, sosIDs []uuid.UUID, within time.Duration) (map[uuid.UUID]bool, error) {
	return audit.RecentObjects(ctx, t.platform, staffID, model.AuditViewStaffSos, model.AuditObjectSos, sosIDs, within)
}
