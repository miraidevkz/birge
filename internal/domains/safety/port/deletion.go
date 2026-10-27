package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MemberEraser clears a member's personal fields in safety on account
// deletion (privacy.DataDeletionRequested, AGENTS.md N6).
type MemberEraser interface {
	// EraseMember, in one transaction: clears the note and the district of
	// every escalation of the member (the row, its IDs, status, timestamps
	// and outcome stay for staff accountability) and records the audit
	// entry once. Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error
}
