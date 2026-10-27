package service

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
)

// ApplyModerationDecided applies moderation.Decided for kind listing.
// Deduplicated via platform.processed_events. Approve → active with closes_at
// once any photo is processed; reject → rejected + reason; notifies the author.
func (s *Service) ApplyModerationDecided(ctx context.Context, eventID uuid.UUID, data model.ModerationDecidedData) error {
	if data.Kind != model.ModerationKindListing {
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
		l, err := tx.GetListing(ctx, data.ObjectID, true)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return nil
			}
			return err
		}
		if l.Status != model.ListingPending {
			return nil
		}
		if data.Approve {
			approved, updated, err := tx.ApproveListing(ctx, l.ID, now)
			if err != nil {
				return err
			}
			if !updated {
				return nil
			}
			if _, err := s.tryActivate(ctx, tx, approved, now); err != nil {
				return err
			}
		} else {
			reason := data.Reason
			if reason == "" {
				reason = "other"
			}
			if utf8.RuneCountInString(reason) > model.MaxRejectReasonLen {
				reason = string([]rune(reason)[:model.MaxRejectReasonLen])
			}
			if _, updated, err := tx.RejectListing(ctx, l.ID, reason); err != nil {
				return err
			} else if !updated {
				return nil
			}
		}
		if err := tx.Publish(ctx, now, model.Delivery(
			l.ID, l.AuthorID, model.TemplateListingDecided,
			map[string]string{"listing_id": l.ID.String()},
		)); err != nil {
			return err
		}
		s.log().Info("listing moderation applied", "event", "ListingModerationApplied", "listing_id", l.ID)
		return nil
	})
}
