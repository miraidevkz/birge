package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"birge/internal/domains/sharing/port"
	"birge/internal/platform/clock"
)

// Deletion erases a member's sharing data on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6): rows in one transaction,
// then the photo objects through the sharing.DeleteObjects timer. It is
// called by the system (the sharing-deletion consumer and the timer), never
// on behalf of a principal.
type Deletion struct {
	Store   port.MemberEraser
	Objects port.ObjectStore
	Clock   clock.Clock
	Logger  *slog.Logger
}

// DeleteMember erases memberID's sharing rows and schedules the object
// deletion. Idempotent.
func (d *Deletion) DeleteMember(ctx context.Context, memberID uuid.UUID) error {
	if memberID == uuid.Nil {
		return errors.New("sharing: delete member: member id is required")
	}
	if err := d.Store.EraseMember(ctx, memberID, d.Clock.Now().UTC()); err != nil {
		return fmt.Errorf("sharing: delete member: %w", err)
	}
	d.log().Info("member data deleted", "event", "MemberDataDeleted", "context", "sharing", "member_id", memberID)
	return nil
}

// DeleteObjects deletes every key from object storage. It stops at the
// first failure and returns it, so the timer retries all keys later; a
// missing object is not an error. A store that is not configured fails
// too: objects written while it was configured must not be left behind.
func (d *Deletion) DeleteObjects(ctx context.Context, keys []string) error {
	if d.Objects == nil {
		return errors.New("sharing: delete objects: object store not configured")
	}
	for _, key := range keys {
		if err := d.Objects.Delete(ctx, key); err != nil {
			return fmt.Errorf("sharing: delete object: %w", err)
		}
	}
	d.log().Info("member objects deleted", "event", "MemberObjectsDeleted", "objects", len(keys))
	return nil
}

func (d *Deletion) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
