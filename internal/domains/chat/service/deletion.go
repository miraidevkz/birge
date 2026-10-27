package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"birge/internal/domains/chat/port"
	"birge/internal/platform/clock"
)

// Deletion erases a member's chat data on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6). Her messages become
// tombstones, like retention, so the other members' history and the
// per-room sequences stay whole; messages under legal hold (ADR-0019) are
// skipped and logged by ID only. It is called by the system (the
// chat-deletion consumer), never on behalf of a principal.
type Deletion struct {
	Store  port.MemberEraser
	Clock  clock.Clock
	Logger *slog.Logger
}

// DeleteMember erases memberID's chat data. Idempotent: a repeated run
// purges nothing new and logs the same held messages again.
func (d *Deletion) DeleteMember(ctx context.Context, memberID uuid.UUID) error {
	if memberID == uuid.Nil {
		return errors.New("chat: delete member: member id is required")
	}
	held, err := d.Store.EraseMember(ctx, memberID, d.Clock.Now().UTC())
	if err != nil {
		return fmt.Errorf("chat: delete member: %w", err)
	}
	for _, id := range held {
		// The record ID only: never the member next to held content.
		d.log().Warn("message under legal hold kept", "event", "DeletionSkippedLegalHold", "message_id", id)
	}
	d.log().Info("member data deleted", "event", "MemberDataDeleted", "context", "chat", "member_id", memberID)
	return nil
}

func (d *Deletion) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
