package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
	"birge/internal/platform/auth"
)

// CreateListing creates a pending listing for a verified member who accepted
// the rules. Emits sharing.ListingSubmitted. When a photo upload is attached,
// schedules sharing.ProcessPhoto; the listing stays pending until the photo
// is processed and moderation approves.
func (s *Service) CreateListing(
	ctx context.Context,
	p auth.Principal,
	idempotencyKey uuid.UUID,
	in model.NewListing,
) (model.ListingView, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.ListingView{}, err
	}
	if idempotencyKey == uuid.Nil {
		return model.ListingView{}, fmt.Errorf("%w: idempotency key", model.ErrInvalidInput)
	}
	if err := validateNewListing(in); err != nil {
		return model.ListingView{}, err
	}
	if err := s.requireVerified(ctx, memberID); err != nil {
		return model.ListingView{}, err
	}

	now := s.now()
	id, err := uuid.NewV7()
	if err != nil {
		return model.ListingView{}, fmt.Errorf("sharing: listing id: %w", err)
	}
	listing := model.Listing{
		ID:             id,
		AuthorID:       memberID,
		IdempotencyKey: idempotencyKey,
		Type:           in.Type,
		Category:       in.Category,
		Title:          strings.TrimSpace(in.Title),
		Description:    strings.TrimSpace(in.Description),
		DistrictID:     in.DistrictID,
		PhotoUploadID:  in.PhotoUploadID,
		Status:         model.ListingPending,
		CreatedAt:      now,
	}

	var out model.Listing
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		if in.PhotoUploadID != nil {
			u, ok, err := tx.MarkPhotoUploadUsed(ctx, *in.PhotoUploadID, memberID, now, now)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: photoUploadId", model.ErrInvalidInput)
			}
			_ = u
		}
		created, inserted, err := tx.InsertListing(ctx, listing)
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := tx.GetListingByIdempotencyKey(ctx, memberID, idempotencyKey)
			if err != nil {
				return err
			}
			out = existing
			return nil
		}
		if err := tx.Publish(ctx, now, model.Event{
			Type:    model.EventListingSubmitted,
			Version: model.EventVersion,
			Data:    model.ListingSubmittedData{ListingID: created.ID},
		}); err != nil {
			return err
		}
		if created.PhotoUploadID != nil {
			if _, err := tx.ScheduleProcessPhoto(ctx, created.ID, now); err != nil {
				return err
			}
		}
		out = created
		return nil
	})
	if err != nil {
		return model.ListingView{}, err
	}
	if out.ID == listing.ID {
		s.log().Info("listing submitted", "event", "ListingSubmitted", "listing_id", out.ID)
	}
	return s.toView(ctx, out, memberID)
}

// ListMyListings returns the caller's listings in every status, newest first.
func (s *Service) ListMyListings(ctx context.Context, p auth.Principal) ([]model.ListingView, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return nil, err
	}
	var rows []model.Listing
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		rows, err = tx.ListMyListings(ctx, memberID, model.MaxMyListings)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.ListingView, 0, len(rows))
	for _, l := range rows {
		v, err := s.toView(ctx, l, memberID)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ListListings returns one page of active listings, newest first. Never the
// viewer's own; blocked authors are dropped both ways (ADR-0019).
func (s *Service) ListListings(
	ctx context.Context,
	p auth.Principal,
	typ, category *string,
	districtID *uuid.UUID,
	cursor string,
) (model.FeedPage, error) {
	viewer, err := memberOf(p)
	if err != nil {
		return model.FeedPage{}, err
	}
	if typ != nil && !model.TypeIsValid(*typ) {
		return model.FeedPage{}, fmt.Errorf("%w: type", model.ErrInvalidInput)
	}
	if category != nil && !model.CodeIsValid(*category) {
		return model.FeedPage{}, fmt.Errorf("%w: category", model.ErrInvalidInput)
	}
	f := model.FeedFilter{
		ViewerID: viewer, Type: typ, Category: category, DistrictID: districtID,
		Limit: model.FeedPageLimit,
	}
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return model.FeedPage{}, err
		}
		f.CursorCreatedAt, f.CursorID = &at, &id
	}
	var rows []model.Listing
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		rows, err = tx.ListActiveListings(ctx, f)
		return err
	}); err != nil {
		return model.FeedPage{}, err
	}
	page := model.FeedPage{Items: []model.ListingView{}}
	if len(rows) == int(f.Limit) {
		last := rows[len(rows)-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	for _, l := range rows {
		if l.AuthorID == viewer {
			s.log().Error("feed row must not be own listing", "event", "ListingFeedFilterBreach", "listing_id", l.ID)
			continue
		}
		b, err := s.blocked(ctx, viewer, l.AuthorID)
		if err != nil {
			return model.FeedPage{}, err
		}
		if b {
			continue
		}
		v, err := s.toView(ctx, l, viewer)
		if err != nil {
			return model.FeedPage{}, err
		}
		page.Items = append(page.Items, v)
	}
	return page, nil
}

// CloseListing closes an active listing of the caller. Idempotent.
func (s *Service) CloseListing(ctx context.Context, p auth.Principal, listingID uuid.UUID) error {
	memberID, err := memberOf(p)
	if err != nil {
		return err
	}
	now := s.now()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		l, err := tx.GetListing(ctx, listingID, true)
		if err != nil {
			return err
		}
		if l.AuthorID != memberID {
			return model.ErrNotFound
		}
		if l.Status == model.ListingClosed {
			return nil
		}
		if l.Status != model.ListingActive {
			return model.ErrConflict
		}
		if _, updated, err := tx.CloseListing(ctx, listingID, now); err != nil {
			return err
		} else if !updated {
			return nil
		}
		if _, err := tx.CancelAutoClose(ctx, listingID, now); err != nil {
			return err
		}
		s.log().Info("listing closed", "event", "ListingClosed", "listing_id", listingID)
		return nil
	})
}

// HandleAutoClose closes an active listing when its timer fires (subject = listing id).
func (s *Service) HandleAutoClose(ctx context.Context, tx port.Tx, listingID uuid.UUID) error {
	now := s.now()
	l, err := tx.GetListing(ctx, listingID, true)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		return err
	}
	if l.Status != model.ListingActive {
		return nil
	}
	if _, updated, err := tx.CloseListing(ctx, listingID, now); err != nil {
		return err
	} else if updated {
		s.log().Info("listing auto-closed", "event", "ListingAutoClosed", "listing_id", listingID)
	}
	return nil
}
