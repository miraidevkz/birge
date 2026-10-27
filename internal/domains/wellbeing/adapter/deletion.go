package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/wellbeing/model"
	"birge/internal/domains/wellbeing/port"
	"birge/internal/domains/wellbeing/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

var _ port.MemberEraser = (*PostgresStore)(nil)

// EraseMember implements port.MemberEraser.
func (s *PostgresStore) EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error {
	return db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		q, platform := store.New(tx), platformstore.New(tx)
		if _, err := timers.CancelSubjects(ctx, platform, now, []string{model.TimerReminder}, []uuid.UUID{memberID}); err != nil {
			return err
		}
		if _, err := q.DeleteMemberSchedule(ctx, memberID); err != nil {
			return fmt.Errorf("wellbeing: delete schedule: %w", err)
		}
		if _, err := q.DeleteMemberCheckins(ctx, memberID); err != nil {
			return fmt.Errorf("wellbeing: delete check-ins: %w", err)
		}
		return audit.RecordMemberDeleted(ctx, platform, "wellbeing", memberID)
	})
}
