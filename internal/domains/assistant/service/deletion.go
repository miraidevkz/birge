package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"birge/internal/domains/assistant/port"
	"birge/internal/platform/clock"
)

// Deletion erases a member's assistant dialogue on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6). It is called by the system
// (the assistant-deletion consumer), never on behalf of a principal, and
// does not touch the crisis path.
type Deletion struct {
	Store  port.MemberEraser
	Clock  clock.Clock
	Logger *slog.Logger
}

// DeleteMember erases memberID's assistant data. Idempotent.
func (d *Deletion) DeleteMember(ctx context.Context, memberID uuid.UUID) error {
	if memberID == uuid.Nil {
		return errors.New("assistant: delete member: member id is required")
	}
	if err := d.Store.EraseMember(ctx, memberID, d.Clock.Now().UTC()); err != nil {
		return fmt.Errorf("assistant: delete member: %w", err)
	}
	d.log().Info("member data deleted", "event", "MemberDataDeleted", "context", "assistant", "member_id", memberID)
	return nil
}

func (d *Deletion) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
