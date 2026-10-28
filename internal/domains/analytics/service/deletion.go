package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/port"
	"birge/internal/platform/clock"
)

// Deletion erases a member's analytics facts on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6). It is called by the system
// (the analytics-deletion consumer), never on behalf of a principal. Key is
// the analytics pseudonym key: analytics finds her facts by computing her
// pseudonym, never by storing her ID.
type Deletion struct {
	Store  port.MemberEraser
	Key    []byte
	Clock  clock.Clock
	Logger *slog.Logger
}

// DeleteMember erases memberID's facts. Idempotent.
func (d *Deletion) DeleteMember(ctx context.Context, memberID uuid.UUID) error {
	if memberID == uuid.Nil {
		return errors.New("analytics: delete member: member id is required")
	}
	if len(d.Key) == 0 {
		return errors.New("analytics: delete member: pseudonym key not configured")
	}
	actor := model.Pseudonym(d.Key, memberID)
	if err := d.Store.EraseActor(ctx, memberID, actor, d.Clock.Now().UTC()); err != nil {
		return fmt.Errorf("analytics: delete member: %w", err)
	}
	d.log().InfoContext(ctx, "member data deleted", "event", "MemberDataDeleted", "context", "analytics", "member_id", memberID)
	return nil
}

func (d *Deletion) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
