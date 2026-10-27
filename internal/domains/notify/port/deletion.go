package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MemberEraser erases a member's notify data on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6).
type MemberEraser interface {
	// EraseMember, in one transaction: deletes her devices, her inbox and
	// every notification whose parameters name her, with their deliveries;
	// cancels the pending delivery timers of those notifications and records
	// the audit entry once. Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error
}
