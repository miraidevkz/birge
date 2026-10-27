package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MemberEraser erases a member's chat data on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6).
type MemberEraser interface {
	// EraseMember, in one transaction: turns every message she wrote into a
	// "message deleted" tombstone (body cleared, purged_at set; the row,
	// its sequence number and author ID stay) except messages under legal
	// hold, which stay untouched and are returned as held; deletes her
	// reports, room memberships, restrictions and mentor assignments;
	// cancels the push timers of the purged messages and records the audit
	// entry once. Direct rooms with her stay for the other member.
	// Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) (held []uuid.UUID, err error)
}
