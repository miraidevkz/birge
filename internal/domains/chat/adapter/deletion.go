package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/domains/chat/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

var _ port.MemberEraser = (*PostgresStore)(nil)

// EraseMember implements port.MemberEraser. The crisis-escalation retry
// timer of a crisis message is left running: it belongs to the crisis path
// (ADR-0023) and only opens a safety escalation, which safety keeps as a
// shell anyway.
func (s *PostgresStore) EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) ([]uuid.UUID, error) {
	var held []uuid.UUID
	err := db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		q, platform := store.New(tx), platformstore.New(tx)
		ids, err := q.ListMemberPurgeableMessageIDs(ctx, memberID)
		if err != nil {
			return fmt.Errorf("chat: list member messages: %w", err)
		}
		if len(ids) > 0 {
			if _, err := timers.CancelSubjects(ctx, platform, now, []string{model.TimerPush}, ids); err != nil {
				return err
			}
			if _, err := q.PurgeMessagesByID(ctx, store.PurgeMessagesByIDParams{Now: &now, Ids: ids}); err != nil {
				return fmt.Errorf("chat: purge member messages: %w", err)
			}
		}
		held, err = q.ListMemberHeldMessageIDs(ctx, memberID)
		if err != nil {
			return fmt.Errorf("chat: list held messages: %w", err)
		}
		if _, err := q.DeleteMemberReports(ctx, memberID); err != nil {
			return fmt.Errorf("chat: delete reports: %w", err)
		}
		if _, err := q.DeleteMemberRoomMemberships(ctx, memberID); err != nil {
			return fmt.Errorf("chat: delete room memberships: %w", err)
		}
		if _, err := q.DeleteMemberRestrictions(ctx, memberID); err != nil {
			return fmt.Errorf("chat: delete restrictions: %w", err)
		}
		if _, err := q.DeleteMemberMentorships(ctx, memberID); err != nil {
			return fmt.Errorf("chat: delete mentor assignments: %w", err)
		}
		return audit.RecordMemberDeleted(ctx, platform, "chat", memberID)
	})
	if err != nil {
		return nil, err
	}
	return held, nil
}
