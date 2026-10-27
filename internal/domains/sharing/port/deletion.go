package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MemberEraser erases a member's sharing data on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6).
type MemberEraser interface {
	// EraseMember, in one transaction: deletes her listings with every
	// contact request on them, her contact requests on other listings and
	// her photo uploads; schedules model.TimerDeleteObjects for the object
	// keys those rows held; cancels the timers of her listings and records
	// the audit entry once. Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error
}
