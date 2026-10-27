package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/port"
	"birge/internal/domains/circle/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

var _ port.MemberEraser = (*PostgresStore)(nil)

// EraseMember implements port.MemberEraser. Requests and matches are locked
// first so the hourly matching run cannot pair a request that is being
// deleted; their IDs are the subjects of the timers to cancel.
func (s *PostgresStore) EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error {
	return db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		q, platform := store.New(tx), platformstore.New(tx)
		requestIDs, err := q.ListMemberTeaRequestIDs(ctx, memberID)
		if err != nil {
			return fmt.Errorf("circle: list member requests: %w", err)
		}
		matchIDs, err := q.ListMemberMatchIDs(ctx, memberID)
		if err != nil {
			return fmt.Errorf("circle: list member matches: %w", err)
		}
		subjects := append(append([]uuid.UUID{}, requestIDs...), matchIDs...)
		if _, err := timers.CancelSubjects(ctx, platform, now,
			[]string{model.TimerRequestExpire, model.TimerAskMet}, subjects); err != nil {
			return err
		}
		if _, err := q.DeleteMemberAcquaintances(ctx, memberID); err != nil {
			return fmt.Errorf("circle: delete acquaintances: %w", err)
		}
		if _, err := q.ExpireMemberProposedMatches(ctx, memberID); err != nil {
			return fmt.Errorf("circle: expire proposed matches: %w", err)
		}
		if _, err := q.DeleteUnmatchedTeaRequests(ctx, memberID); err != nil {
			return fmt.Errorf("circle: delete tea requests: %w", err)
		}
		if _, err := q.CancelMatchedTeaRequests(ctx, memberID); err != nil {
			return fmt.Errorf("circle: cancel matched tea requests: %w", err)
		}
		return audit.RecordMemberDeleted(ctx, platform, "circle", memberID)
	})
}
