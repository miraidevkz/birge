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
	// contact, her plan sends and her external plan and SOS links; cancels
	// the auto-close timers of her requests and the purge timers of her links
	// and records the audit entry once. Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error
}
