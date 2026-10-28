package service

import (
	"context"
	"time"

	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/port"
)

// retentionAt is the time of day (UTC) of the daily sweep.
const retentionAt = 3*time.Hour + 30*time.Minute

// EnsureRetentionTimer schedules the next daily analytics.Retention run. The
// worker calls it on start; the dedup key (one per day) makes it idempotent.
func (s *Service) EnsureRetentionTimer(ctx context.Context) error {
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		return tx.ScheduleRetention(ctx, model.NextDailyRun(s.now(), retentionAt))
	})
}

// HandleRetention runs the analytics.Retention timer in the worker's
// transaction: facts older than FactRetentionMonths, cut at the Monday that
// starts their week in the deployment zone (so a week is rolled up whole and
// once), become weekly counts without actors and are deleted; erased
// pseudonyms older than ErasedRetention are deleted; the next run is
// scheduled. Everything commits with the timer, so a crash repeats it whole.
func (s *Service) HandleRetention(ctx context.Context, tx port.Tx) error {
	loc, err := s.location()
	if err != nil {
		return err
	}
	now := s.now()
	cutoff := model.RetentionCutoff(now, loc)
	deleted, err := tx.RollUpAndDelete(ctx, loc.String(), cutoff, now.Add(-model.ErasedRetention))
	if err != nil {
		return err
	}
	s.log().InfoContext(ctx, "analytics retention sweep", "event", "AnalyticsRetention", "deleted", deleted)
	return tx.ScheduleRetention(ctx, model.NextDailyRun(now, retentionAt))
}
