package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"birge/internal/domains/circle/port"
	"birge/internal/platform/clock"
)

// Deletion erases a member's circle data on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6). Matches with her stay as
// tombstones: the other member's history keeps the pair, and her name is
// read from membership, which no longer knows her ("deleted"). It is called
// by the system (the circle-deletion consumer), never on behalf of a principal.
type Deletion struct {
	Store  port.MemberEraser
	Clock  clock.Clock
	Logger *slog.Logger
}

// DeleteMember erases memberID's circle data. Idempotent.
func (d *Deletion) DeleteMember(ctx context.Context, memberID uuid.UUID) error {
	if memberID == uuid.Nil {
		return errors.New("circle: delete member: member id is required")
	}
	if err := d.Store.EraseMember(ctx, memberID, d.Clock.Now().UTC()); err != nil {
		return fmt.Errorf("circle: delete member: %w", err)
	}
	d.log().Info("member data deleted", "event", "MemberDataDeleted", "context", "circle", "member_id", memberID)
	return nil
}

func (d *Deletion) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
