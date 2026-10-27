package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
)

// ModerationSummary returns title, description and processed photo URL for a
// listing under review (ContentSource). Gone listing → model.ErrNotFound.
// No contact data.
func (s *Service) ModerationSummary(ctx context.Context, listingID uuid.UUID) (text, photoURL string, err error) {
	if listingID == uuid.Nil {
		return "", "", model.ErrNotFound
	}
	var l model.Listing
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		l, err = tx.GetListing(ctx, listingID, false)
		return err
	})
	if errors.Is(err, model.ErrNotFound) {
		return "", "", model.ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	text = strings.TrimSpace(l.Title)
	if desc := strings.TrimSpace(l.Description); desc != "" {
		if text != "" {
			text += "\n\n"
		}
		text += desc
	}
	photoURL, err = s.photoURL(ctx, l.PhotoObjectKey)
	if err != nil {
		// Photo URL is optional for the queue; a store outage should not hide the text.
		s.log().WarnContext(ctx, "listing photo url for moderation", "event", "SharingModerationPhotoURLFailed", "listing_id", listingID)
		photoURL = ""
	}
	return text, photoURL, nil
}
