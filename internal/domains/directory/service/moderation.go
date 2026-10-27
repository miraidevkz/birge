package service

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"birge/internal/domains/directory/model"
	"birge/internal/domains/directory/port"
)

// ApplyModerationDecided applies moderation.Decided for kinds specialist and
// review. Deduplicated via platform.processed_events.
func (s *Service) ApplyModerationDecided(ctx context.Context, eventID uuid.UUID, data model.ModerationDecidedData) error {
	switch data.Kind {
	case model.ModerationKindSpecialist, model.ModerationKindReview:
	default:
		return nil
	}
	if eventID == uuid.Nil || data.ObjectID == uuid.Nil {
		return fmt.Errorf("%w: moderation event", model.ErrInvalidInput)
	}
	now := s.now()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		newly, err := tx.MarkProcessed(ctx, model.ConsumerModerationDecided, eventID)
		if err != nil {
			return err
		}
		if !newly {
			return nil
		}
		reason := data.Reason
		if !data.Approve {
			if reason == "" {
				reason = "other"
			}
			if utf8.RuneCountInString(reason) > model.MaxRejectReasonLen {
				reason = string([]rune(reason)[:model.MaxRejectReasonLen])
			}
		}
		switch data.Kind {
		case model.ModerationKindSpecialist:
			return applySpecialist(ctx, tx, data, reason)
		case model.ModerationKindReview:
			return applyReview(ctx, tx, data, reason, now)
		}
		return nil
	})
}

func applySpecialist(ctx context.Context, tx port.Tx, data model.ModerationDecidedData, reason string) error {
	sp, err := tx.GetSpecialist(ctx, data.ObjectID, true)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		return err
	}
	if sp.Status != model.StatusPending {
		return nil
	}
	if data.Approve {
		_, _, err = tx.ApproveSpecialist(ctx, sp.ID)
	} else {
		_, _, err = tx.RejectSpecialist(ctx, sp.ID, reason)
	}
	return err
}

func applyReview(ctx context.Context, tx port.Tx, data model.ModerationDecidedData, reason string, now time.Time) error {
	r, err := tx.GetReview(ctx, data.ObjectID, true)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		return err
	}
	if r.Status != model.StatusPending {
		return nil
	}
	if data.Approve {
		_, _, err = tx.ApproveReview(ctx, r.ID, now)
	} else {
		_, _, err = tx.RejectReview(ctx, r.ID, reason, now)
	}
	return err
}
