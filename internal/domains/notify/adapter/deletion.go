package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/port"
	"birge/internal/domains/notify/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

var _ port.MemberEraser = (*PostgresStore)(nil)

// EraseMember implements port.MemberEraser. Notifications are locked and
// listed first so the deliveries (foreign key) go before their
// notifications and the timers of exactly those rows are cancelled.
func (s *PostgresStore) EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error {
	return db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		q, platform := store.New(tx), platformstore.New(tx)
		ids, err := q.ListMemberNotificationIDs(ctx, store.ListMemberNotificationIDsParams{
			MemberID: memberID, MemberRef: memberID.String(),
		})
		if err != nil {
			return fmt.Errorf("notify: list member notifications: %w", err)
		}
		if len(ids) > 0 {
			if _, err := timers.CancelSubjects(ctx, platform, now,
				[]string{model.TimerPush, model.TimerCriticalSMS, model.TimerExternalSMS}, ids); err != nil {
				return err
			}
			if _, err := q.DeleteNotificationDeliveries(ctx, ids); err != nil {
				return fmt.Errorf("notify: delete deliveries: %w", err)
			}
			if _, err := q.DeleteNotifications(ctx, ids); err != nil {
				return fmt.Errorf("notify: delete notifications: %w", err)
			}
		}
		if _, err := q.DeleteMemberDevices(ctx, memberID); err != nil {
			return fmt.Errorf("notify: delete devices: %w", err)
		}
		return audit.RecordMemberDeleted(ctx, platform, "notify", memberID)
	})
}
