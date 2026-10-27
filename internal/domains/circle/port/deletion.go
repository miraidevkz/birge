package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MemberEraser erases a member's circle data on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6).
type MemberEraser interface {
	// EraseMember, in one transaction: deletes her acquaintances and every
	// tea request no match references; keeps her matches as tombstones for
	// the other member's history (a proposal ends as expired, a matched
	// open request is cancelled); cancels the timers of her requests and
	// matches and records the audit entry once. Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error
}
