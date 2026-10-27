package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
	"birge/internal/platform/auth"
)

// CreatePhotoUpload reserves a presigned PUT slot for one item photo
// (JPEG/PNG, ≤ 5 MB, 10-minute expiry). Verified members only.
func (s *Service) CreatePhotoUpload(
	ctx context.Context,
	p auth.Principal,
	contentType string,
	sizeBytes int,
) (model.PhotoUploadSlot, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.PhotoUploadSlot{}, err
	}
	if !model.ContentTypeIsValid(contentType) {
		return model.PhotoUploadSlot{}, fmt.Errorf("%w: contentType", model.ErrInvalidInput)
	}
	if sizeBytes < 1 || sizeBytes > model.MaxPhotoBytes {
		return model.PhotoUploadSlot{}, fmt.Errorf("%w: sizeBytes", model.ErrInvalidInput)
	}
	if err := s.requireVerified(ctx, memberID); err != nil {
		return model.PhotoUploadSlot{}, err
	}
	if s.Objects == nil {
		return model.PhotoUploadSlot{}, errors.New("sharing: object store not configured")
	}

	now := s.now()
	id, err := uuid.NewV7()
	if err != nil {
		return model.PhotoUploadSlot{}, fmt.Errorf("sharing: upload id: %w", err)
	}
	key := model.UploadObjectKey(id)
	expires := now.Add(model.PhotoUploadTTL)
	url, err := s.Objects.PresignPut(ctx, key, contentType, model.PhotoUploadTTL)
	if err != nil {
		return model.PhotoUploadSlot{}, fmt.Errorf("sharing: presign put: %w", err)
	}

	upload := model.PhotoUpload{
		ID: id, MemberID: memberID, ObjectKey: key, ContentType: contentType,
		SizeBytes: sizeBytes, ExpiresAt: expires, Status: model.PhotoPending, CreatedAt: now,
	}
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		_, err := tx.InsertPhotoUpload(ctx, upload)
		return err
	})
	if err != nil {
		return model.PhotoUploadSlot{}, err
	}
	s.log().Info("photo upload created", "event", "PhotoUploadCreated", "upload_id", id)
	return model.PhotoUploadSlot{UploadID: id, URL: url, ExpiresAt: expires}, nil
}

// HandleProcessPhoto re-encodes the listing's photo, strips metadata, stores
// the processed object, deletes the original and tries to activate the listing
// when moderation has already approved (ADR-0015). Subject = listing id.
func (s *Service) HandleProcessPhoto(ctx context.Context, listingID uuid.UUID) error {
	if s.Objects == nil || s.Images == nil {
		return errors.New("sharing: object store or image processor not configured")
	}

	var listing model.Listing
	var upload model.PhotoUpload
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		listing, err = tx.GetListing(ctx, listingID, true)
		if err != nil {
			return err
		}
		if listing.PhotoUploadID == nil {
			return nil
		}
		if listing.PhotoObjectKey != nil {
			_, err = s.tryActivate(ctx, tx, listing, s.now())
			return err
		}
		upload, err = tx.GetPhotoUpload(ctx, *listing.PhotoUploadID, true)
		return err
	})
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		return err
	}
	if listing.PhotoUploadID == nil || listing.PhotoObjectKey != nil {
		return nil
	}
	if upload.Status == model.PhotoProcessed && upload.ProcessedObjectKey != nil {
		return s.Store.InTx(ctx, func(tx port.Tx) error {
			l, err := tx.SetListingPhotoObjectKey(ctx, listing.ID, *upload.ProcessedObjectKey)
			if err != nil {
				return err
			}
			_, err = s.tryActivate(ctx, tx, l, s.now())
			return err
		})
	}
	if upload.Status == model.PhotoFailed {
		return nil
	}

	raw, err := s.Objects.Get(ctx, upload.ObjectKey)
	if err != nil {
		// Object may not be present yet (client still uploading); retry the timer.
		return fmt.Errorf("sharing: get upload: %w", err)
	}
	processed, err := s.Images.Process(ctx, raw)
	if err != nil {
		_ = s.Store.InTx(ctx, func(tx port.Tx) error {
			_, _, markErr := tx.MarkPhotoUploadFailed(ctx, upload.ID)
			return markErr
		})
		s.log().Warn("listing photo failed", "event", "ListingPhotoFailed", "upload_id", upload.ID)
		if errors.Is(err, model.ErrPhoto) {
			return nil // permanent decode failure; do not retry
		}
		return err
	}
	processedKey := model.ProcessedObjectKey(listing.ID)
	if err := s.Objects.Put(ctx, processedKey, processed, model.ContentJPEG); err != nil {
		return fmt.Errorf("sharing: put processed: %w", err)
	}

	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		if _, updated, err := tx.MarkPhotoUploadProcessed(ctx, upload.ID, processedKey); err != nil {
			return err
		} else if !updated {
			return nil
		}
		l, err := tx.SetListingPhotoObjectKey(ctx, listing.ID, processedKey)
		if err != nil {
			return err
		}
		_, err = s.tryActivate(ctx, tx, l, s.now())
		return err
	})
	if err != nil {
		return err
	}
	// Best-effort: the processed object is the source of truth after commit.
	if delErr := s.Objects.Delete(ctx, upload.ObjectKey); delErr != nil {
		s.log().Warn("original photo delete failed", "event", "PhotoOriginalDeleteFailed", "listing_id", listingID)
	}
	s.log().Info("listing photo processed", "event", "ListingPhotoProcessed", "listing_id", listingID)
	return nil
}
