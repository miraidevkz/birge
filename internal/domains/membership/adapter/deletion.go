package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/port"
	"birge/internal/domains/membership/service"
	"birge/internal/domains/membership/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

var _ port.MemberEraser = (*Postgres)(nil)

// EraseMember implements port.MemberEraser. The member row is locked first,
// so a concurrent request of the same identity waits and then sees the
// tombstone. The identity timer is scheduled only by the run that turns the
// row into a tombstone, so a repeated run schedules nothing.
func (p *Postgres) EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error {
	return db.InTx(ctx, p.Pool, func(tx pgx.Tx) error {
		q, platform := store.New(tx), platformstore.New(tx)
		row, err := q.LockMemberForDeletion(ctx, memberID)
		if errors.Is(err, pgx.ErrNoRows) {
			// No member row: nothing to erase; the entry still records that membership ran.
			return audit.RecordMemberDeleted(ctx, platform, model.BoundedContext, memberID)
		}
		if err != nil {
			return fmt.Errorf("membership: lock member for deletion: %w", err)
		}
		if _, err := q.DeleteMemberChildren(ctx, memberID); err != nil {
			return fmt.Errorf("membership: delete children: %w", err)
		}
		if _, err := q.DeleteMemberVerificationRequests(ctx, memberID); err != nil {
			return fmt.Errorf("membership: delete verification requests: %w", err)
		}
		if _, err := q.DeleteMemberBlocks(ctx, memberID); err != nil {
			return fmt.Errorf("membership: delete blocks: %w", err)
		}
		n, err := q.TombstoneMember(ctx, store.TombstoneMemberParams{Now: now, ID: memberID})
		if err != nil {
			return fmt.Errorf("membership: tombstone member: %w", err)
		}
		if n == 1 {
			if _, err := timers.Schedule(ctx, platform, timers.Timer{
				Kind: model.TimerDeleteIdentity, SubjectID: row.IdentityID,
				DedupKey: row.IdentityID.String(), DueAt: now, Payload: map[string]string{},
			}); err != nil {
				return err
			}
		}
		return audit.RecordMemberDeleted(ctx, platform, model.BoundedContext, memberID)
	})
}

// TimerHandlers adapts platform timers to membership service methods.
type TimerHandlers struct {
	Deletion *service.Deletion
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerDeleteIdentity: h.DeleteIdentity,
	}
}

// DeleteIdentity handles membership.DeleteIdentity (subject = identity ID).
// The HTTP call runs inside the worker's transaction only to keep the timer
// claimed; it touches no table, and a failure reschedules the timer.
func (h *TimerHandlers) DeleteIdentity(ctx context.Context, _ pgx.Tx, due timers.Due) error {
	return h.Deletion.DeleteIdentity(ctx, due.SubjectID)
}
