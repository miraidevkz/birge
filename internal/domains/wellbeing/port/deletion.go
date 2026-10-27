package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MemberEraser erases a member's wellbeing data on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6).
type MemberEraser interface {
	// EraseMember, in one transaction: deletes her reminder schedule and
	// check-ins, cancels her pending reminder timer and records the audit
	// entry once. Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error
}
