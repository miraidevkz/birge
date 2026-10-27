package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"birge/internal/domains/safety/port"
	"birge/internal/platform/clock"
)

// Deletion handles account deletion in safety (privacy.DataDeletionRequested,
// AGENTS.md N6). Escalations are kept as shells for staff accountability:
// IDs, source, status, timestamps and outcome stay; the note and the
// district are cleared. It is called by the system (the safety-deletion
// consumer), never on behalf of a principal, and it never touches the crisis
// path: open escalations stay open and their backup timers keep running.
type Deletion struct {
	Store  port.MemberEraser
	Clock  clock.Clock
	Logger *slog.Logger
}

// DeleteMember clears memberID's personal fields in safety. Idempotent.
func (d *Deletion) DeleteMember(ctx context.Context, memberID uuid.UUID) error {
	if memberID == uuid.Nil {
		return errors.New("safety: delete member: member id is required")
	}
	if err := d.Store.EraseMember(ctx, memberID, d.Clock.Now().UTC()); err != nil {
		return fmt.Errorf("safety: delete member: %w", err)
	}
	d.log().Info("member data deleted", "event", "MemberDataDeleted", "context", "safety", "member_id", memberID)
	return nil
}

func (d *Deletion) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
