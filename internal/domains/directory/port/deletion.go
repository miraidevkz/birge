package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MemberEraser erases a member's directory data on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6).
type MemberEraser interface {
	// EraseMember, in one transaction: deletes her reviews and the
	// specialist cards she suggested that are not active and that nobody
	// reviewed; keeps the other cards she suggested (they describe the
	// specialist, not her, and other members use them) and records the
	// audit entry once. Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error
}
