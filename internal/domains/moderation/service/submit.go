package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/port"
)

// HandleEvent turns one consumed event into a queue item. Unknown types are
// ignored. A malformed payload returns model.ErrInvalidInput (redelivery
// cannot fix it). Safe to call with duplicates: the event ID is recorded in
// platform.processed_events in the same transaction as the item. It needs
// only the moderation database, never another context.
func (s *Service) HandleEvent(ctx context.Context, eventID uuid.UUID, eventType string, data json.RawMessage) error {
	sub, ok, err := submissionOf(eventType, data)
	if err != nil || !ok {
		return err
	}
	return s.Submit(ctx, eventID, sub)
}

func submissionOf(eventType string, data json.RawMessage) (model.Submission, bool, error) {
	bad := func(what string) (model.Submission, bool, error) {
		return model.Submission{}, false, fmt.Errorf("%w: %s %s", model.ErrInvalidInput, eventType, what)
	}
	switch eventType {
	case model.EventListingSubmitted:
		var d model.ListingSubmittedData
		if err := json.Unmarshal(data, &d); err != nil {
			return bad("payload")
		}
		return model.Submission{Kind: model.KindListing, ObjectID: d.ListingID}, true, nil
	case model.EventItemSubmitted:
		var d model.ItemSubmittedData
		if err := json.Unmarshal(data, &d); err != nil {
			return bad("payload")
		}
		k := model.Kind(d.ObjectType)
		if k != model.KindSpecialist && k != model.KindReview {
			return bad("object_type")
		}
		return model.Submission{Kind: k, ObjectID: d.ObjectID}, true, nil
	case model.EventMessageHeld:
		var d model.MessageHeldData
		if err := json.Unmarshal(data, &d); err != nil {
			return bad("payload")
		}
		return model.Submission{Kind: model.KindChatMessage, ObjectID: d.MessageID, SourceReason: d.Reason}, true, nil
	case model.EventMessageReported:
		var d model.MessageReportedData
		if err := json.Unmarshal(data, &d); err != nil {
			return bad("payload")
		}
		return model.Submission{Kind: model.KindReport, ObjectID: d.MessageID, SourceReason: d.Reason}, true, nil
	case model.EventVerificationRequested:
		var d model.VerificationRequestedData
		if err := json.Unmarshal(data, &d); err != nil {
			return bad("payload")
		}
		// One item per request: the request is the object membership decides.
		return model.Submission{Kind: model.KindVerification, ObjectID: d.RequestID}, true, nil
	}
	return model.Submission{}, false, nil
}

// Submit opens the queue item of sub, deduplicated by eventID. An object
// with an open item gets no second one; a report on a message with an open
// report item increments its counter instead.
func (s *Service) Submit(ctx context.Context, eventID uuid.UUID, sub model.Submission) error {
	if eventID == uuid.Nil || sub.ObjectID == uuid.Nil || !sub.Kind.Valid() {
		return fmt.Errorf("%w: submission", model.ErrInvalidInput)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("moderation: item id: %w", err)
	}
	it := model.Item{
		ID: id, Kind: sub.Kind, ObjectID: sub.ObjectID, Status: model.StatusOpen,
		SourceReason: model.SourceReason(sub.SourceReason), ReportCount: 1, CreatedAt: s.now(),
	}
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		newly, err := tx.MarkProcessed(ctx, model.ConsumerQueue, eventID)
		if err != nil || !newly {
			return err
		}
		if sub.Kind == model.KindReport {
			itemID, count, err := tx.UpsertReportItem(ctx, it)
			if err != nil {
				return err
			}
			s.log().InfoContext(ctx, "moderation report queued", "event", "ModerationReportQueued", "item_id", itemID, "report_count", count)
			return nil
		}
		created, err := tx.InsertItem(ctx, it)
		if err != nil {
			return err
		}
		if created {
			s.log().InfoContext(ctx, "moderation item opened", "event", "ModerationItemOpened", "item_id", it.ID, "kind", it.Kind)
		}
		return nil
	})
}
