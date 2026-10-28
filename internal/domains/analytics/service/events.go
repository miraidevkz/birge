package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/port"
)

// HandleEvent stores the facts of one consumed event. Safe with duplicates:
// the event ID is recorded in platform.processed_events in the same
// transaction as the facts. Unknown types are ignored. A payload that cannot
// be read returns model.ErrInvalidInput (redelivery cannot fix it). Facts
// older than the retention period are not stored: the next sweep would only
// delete them again. It needs only the analytics database.
func (s *Service) HandleEvent(ctx context.Context, e model.Event) error {
	if len(s.Settings.Key) == 0 {
		return errors.New("analytics: pseudonym key not configured")
	}
	if e.ID == uuid.Nil {
		return fmt.Errorf("%w: event id", model.ErrInvalidInput)
	}
	loc, err := s.location()
	if err != nil {
		return err
	}
	facts, err := model.FactsFromEvent(s.Settings.Key, e, loc)
	if err != nil || len(facts) == 0 {
		return err
	}
	oldest := s.now().AddDate(0, -model.FactRetentionMonths, 0)
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		newly, err := tx.MarkProcessed(ctx, FactsConsumer, e.ID)
		if err != nil || !newly {
			return err
		}
		for _, f := range facts {
			if f.OccurredAt.Before(oldest) {
				continue
			}
			if f.ID, err = uuid.NewV7(); err != nil {
				return fmt.Errorf("analytics: fact id: %w", err)
			}
			if err := tx.InsertFact(ctx, f); err != nil {
				return err
			}
		}
		return nil
	})
}
