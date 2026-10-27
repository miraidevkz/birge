// Package port defines the interfaces the sharing service needs: its store
// (listings, photo uploads, contact requests, outbox, timers, processed_events),
// object storage for item photos, image processing, and what it reads from
// other contexts (member profiles and blocks from membership). Adapters for
// the other contexts are wired later; tests use the fakes in sharingtest.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/sharing/model"
)

// MemberInfo reads member profiles owned by membership. Results are never
// logged, stored or put into events.
type MemberInfo interface {
	Profiles(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]model.Profile, error)
}

// Blocks reads member blocks owned by membership (ADR-0019).
type Blocks interface {
	// IsBlocked reports whether a blocked b or b blocked a.
	IsBlocked(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// ObjectStore is the S3-compatible object storage port (ADR-0015). The
// production adapter is platform/objectstore; tests use an in-memory fake.
type ObjectStore interface {
	// PresignPut returns a temporary URL the client may PUT the raw bytes to.
	// contentLength is required so the signature can bind Content-Length.
	PresignPut(ctx context.Context, key, contentType string, contentLength int64, expires time.Duration) (url string, err error)
	// PresignGet returns a temporary (or public) URL for reading a processed object.
	PresignGet(ctx context.Context, key string, expires time.Duration) (url string, err error)
	// Get returns the object bytes.
	Get(ctx context.Context, key string) ([]byte, error)
	// Put stores bytes under key with the given content type.
	Put(ctx context.Context, key string, body []byte, contentType string) error
	// Delete removes the object; missing keys are not an error.
	Delete(ctx context.Context, key string) error
}

// ImageProcessor re-encodes an uploaded image as JPEG, strips metadata and
// downscales to at most MaxPhotoDimension on the long side (ADR-0015).
type ImageProcessor interface {
	// Process decodes raw (JPEG or PNG), re-encodes as JPEG without metadata,
	// and returns the bytes. Non-decodable input returns model.ErrPhoto.
	Process(ctx context.Context, raw []byte) ([]byte, error)
}

// Store is the persistence port for sharing tables and platform hooks.
type Store interface {
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction.
type Tx interface {
	InsertListing(ctx context.Context, l model.Listing) (created model.Listing, inserted bool, err error)
	GetListingByIdempotencyKey(ctx context.Context, authorID, key uuid.UUID) (model.Listing, error)
	GetListing(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Listing, error)
	ListMyListings(ctx context.Context, authorID uuid.UUID, limit int32) ([]model.Listing, error)
	ListActiveListings(ctx context.Context, f model.FeedFilter) ([]model.Listing, error)
	SetListingPhotoObjectKey(ctx context.Context, id uuid.UUID, key string) (model.Listing, error)
	ApproveListing(ctx context.Context, id uuid.UUID, at time.Time) (l model.Listing, updated bool, err error)
	ActivateListing(ctx context.Context, id uuid.UUID, closesAt time.Time) (l model.Listing, updated bool, err error)
	RejectListing(ctx context.Context, id uuid.UUID, reason string) (l model.Listing, updated bool, err error)
	CloseListing(ctx context.Context, id uuid.UUID, at time.Time) (l model.Listing, updated bool, err error)

	InsertPhotoUpload(ctx context.Context, p model.PhotoUpload) (model.PhotoUpload, error)
	GetPhotoUpload(ctx context.Context, id uuid.UUID, forUpdate bool) (model.PhotoUpload, error)
	MarkPhotoUploadUsed(ctx context.Context, id, memberID uuid.UUID, usedAt, now time.Time) (p model.PhotoUpload, ok bool, err error)
	MarkPhotoUploadProcessed(ctx context.Context, id uuid.UUID, processedKey string) (p model.PhotoUpload, updated bool, err error)
	MarkPhotoUploadFailed(ctx context.Context, id uuid.UUID) (p model.PhotoUpload, updated bool, err error)

	InsertContactRequest(ctx context.Context, c model.ContactRequest) (created model.ContactRequest, inserted bool, err error)
	GetContactRequestByListingRequester(ctx context.Context, listingID, requesterID uuid.UUID) (model.ContactRequest, error)
	GetContactRequest(ctx context.Context, id uuid.UUID, forUpdate bool) (model.ContactRequest, error)
	AcceptContactRequest(ctx context.Context, id uuid.UUID, at time.Time) (c model.ContactRequest, updated bool, err error)

	Publish(ctx context.Context, now time.Time, e model.Event) error
	MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (newly bool, err error)

	ScheduleProcessPhoto(ctx context.Context, listingID uuid.UUID, dueAt time.Time) (scheduled bool, err error)
	ScheduleAutoClose(ctx context.Context, listingID uuid.UUID, dueAt time.Time) (scheduled bool, err error)
	CancelAutoClose(ctx context.Context, listingID uuid.UUID, at time.Time) (int64, error)
}
