package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MemberEraser erases a member's emergency data on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6).
type MemberEraser interface {
	// EraseMember, in one transaction: deletes her SOS requests with every
	// response to them, her responses to other requests, her child card,
	// her trusted contacts and the entries in which she is another owner's
	// contact, and her plan sends; cancels the auto-close timers of her
	// requests and records the audit entry once. Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error
}
