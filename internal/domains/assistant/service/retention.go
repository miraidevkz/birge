package service

import (
	"context"
	"time"

	"birge/internal/domains/assistant/port"
)

// EnsureRetentionPurge schedules the daily assistant.RetentionPurge timer
// when none is active (the dedup key keeps it a singleton across replicas
// and restarts). Wiring calls it at start-up.
func (s *Service) EnsureRetentionPurge(ctx context.Context) error {
	now := s.Clock.Now().UTC()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		_, err := tx.ScheduleRetentionPurge(ctx, now)
		return err
	})
}

// HandleRetentionPurgeTx deletes one batch of turns older than the retention
// period on the timer worker's transaction and schedules the next run: in
// 24 hours, or immediately when the batch was full and more rows may remain.
// Repeating it is harmless: it only deletes what is already past retention.
func (s *Service) HandleRetentionPurgeTx(ctx context.Context, tx port.Tx) error {
	now := s.Clock.Now().UTC()
	days := orDefault(s.Settings.RetentionDays, DefaultRetentionDays)
	batch := orDefault(s.Settings.PurgeBatchSize, DefaultPurgeBatchSize)
	cutoff := now.AddDate(0, 0, -days)
	deleted, err := tx.PurgeTurnsBefore(ctx, cutoff, batch)
	if err != nil {
		return err
	}
	next := now.Add(24 * time.Hour)
	if deleted >= int64(batch) {
		next = now
	}
	// The claimed timer is still active; cancel it so the dedup key is free.
	if _, err := tx.CancelRetentionPurge(ctx, now); err != nil {
		return err
	}
	if _, err := tx.ScheduleRetentionPurge(ctx, next); err != nil {
		return err
	}
	s.log().Info("assistant retention purge", "event", "AssistantRetentionPurged", "deleted", deleted)
	return nil
}
