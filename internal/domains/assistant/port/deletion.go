package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MemberEraser erases a member's assistant dialogue on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6).
type MemberEraser interface {
	// EraseMember deletes every turn of her dialogue (before the 90-day
	// retention would) and records the audit entry once, in one
	// transaction. Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error
}
