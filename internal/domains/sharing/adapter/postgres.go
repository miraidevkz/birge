// Package adapter implements the sharing ports on PostgreSQL (schema sharing,
// sqlc in ../store) and the platform hooks it needs: outbox events, timers
// and processed_events. Object storage and membership ports are wired later.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
	sharingstore "birge/internal/domains/sharing/store"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// PostgresStore implements port.Store on PostgreSQL.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// InTx runs fn in a transaction.
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(Bind(pgxTx))
	})
}

// Bind returns a port.Tx working inside an existing transaction.
func Bind(tx pgx.Tx) port.Tx {
	return &postgresTx{q: sharingstore.New(tx), platform: platformstore.New(tx)}
}

type postgresTx struct {
	q        *sharingstore.Queries
	platform *platformstore.Queries
}

func notFound(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}

// InsertListing implements port.Tx.
func (t *postgresTx) InsertListing(ctx context.Context, l model.Listing) (model.Listing, bool, error) {
	row, err := t.q.InsertListing(ctx, sharingstore.InsertListingParams{
		ID: l.ID, AuthorID: l.AuthorID, IdempotencyKey: l.IdempotencyKey, Type: l.Type,
		Category: l.Category, Title: l.Title, Description: l.Description, DistrictID: l.DistrictID,
		PhotoUploadID: l.PhotoUploadID, CreatedAt: l.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Listing{}, false, nil
	}
	if err != nil {
		return model.Listing{}, false, fmt.Errorf("insert listing: %w", err)
	}
	return mapListing(row), true, nil
}

// GetListingByIdempotencyKey implements port.Tx.
func (t *postgresTx) GetListingByIdempotencyKey(ctx context.Context, authorID, key uuid.UUID) (model.Listing, error) {
	row, err := t.q.GetListingByIdempotencyKey(ctx, sharingstore.GetListingByIdempotencyKeyParams{
		AuthorID: authorID, IdempotencyKey: key,
	})
	if err != nil {
		return model.Listing{}, notFound(err, "get listing by key")
	}
	return mapListing(row), nil
}

// GetListing implements port.Tx.
func (t *postgresTx) GetListing(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Listing, error) {
	var row sharingstore.SharingListing
	var err error
	if forUpdate {
		row, err = t.q.GetListingForUpdate(ctx, id)
	} else {
		row, err = t.q.GetListing(ctx, id)
	}
	if err != nil {
		return model.Listing{}, notFound(err, "get listing")
	}
	return mapListing(row), nil
}

// ListMyListings implements port.Tx.
func (t *postgresTx) ListMyListings(ctx context.Context, authorID uuid.UUID, limit int32) ([]model.Listing, error) {
	rows, err := t.q.ListMyListings(ctx, sharingstore.ListMyListingsParams{AuthorID: authorID, PageLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list my listings: %w", err)
	}
	out := make([]model.Listing, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapListing(r))
	}
	return out, nil
}

// ListActiveListings implements port.Tx.
func (t *postgresTx) ListActiveListings(ctx context.Context, f model.FeedFilter) ([]model.Listing, error) {
	rows, err := t.q.ListActiveListings(ctx, sharingstore.ListActiveListingsParams{
		FilterType: f.Type, FilterCategory: f.Category, FilterDistrict: f.DistrictID,
		ViewerID: f.ViewerID, CursorCreatedAt: f.CursorCreatedAt, CursorID: f.CursorID, PageLimit: f.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list active listings: %w", err)
	}
	out := make([]model.Listing, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapListing(r))
	}
	return out, nil
}

// SetListingPhotoObjectKey implements port.Tx.
func (t *postgresTx) SetListingPhotoObjectKey(ctx context.Context, id uuid.UUID, key string) (model.Listing, error) {
	row, err := t.q.SetListingPhotoObjectKey(ctx, sharingstore.SetListingPhotoObjectKeyParams{
		PhotoObjectKey: &key, ID: id,
	})
	if err != nil {
		return model.Listing{}, notFound(err, "set photo key")
	}
	return mapListing(row), nil
}

// ApproveListing implements port.Tx.
func (t *postgresTx) ApproveListing(ctx context.Context, id uuid.UUID, at time.Time) (model.Listing, bool, error) {
	row, err := t.q.ApproveListing(ctx, sharingstore.ApproveListingParams{ApprovedAt: &at, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Listing{}, false, nil
	}
	if err != nil {
		return model.Listing{}, false, fmt.Errorf("approve listing: %w", err)
	}
	return mapListing(row), true, nil
}

// ActivateListing implements port.Tx.
func (t *postgresTx) ActivateListing(ctx context.Context, id uuid.UUID, closesAt time.Time) (model.Listing, bool, error) {
	row, err := t.q.ActivateListing(ctx, sharingstore.ActivateListingParams{ClosesAt: &closesAt, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Listing{}, false, nil
	}
	if err != nil {
		return model.Listing{}, false, fmt.Errorf("activate listing: %w", err)
	}
	return mapListing(row), true, nil
}

// RejectListing implements port.Tx.
func (t *postgresTx) RejectListing(ctx context.Context, id uuid.UUID, reason string) (model.Listing, bool, error) {
	row, err := t.q.RejectListing(ctx, sharingstore.RejectListingParams{RejectReason: &reason, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Listing{}, false, nil
	}
	if err != nil {
		return model.Listing{}, false, fmt.Errorf("reject listing: %w", err)
	}
	return mapListing(row), true, nil
}

// CloseListing implements port.Tx.
func (t *postgresTx) CloseListing(ctx context.Context, id uuid.UUID, at time.Time) (model.Listing, bool, error) {
	row, err := t.q.CloseListing(ctx, sharingstore.CloseListingParams{ClosedAt: &at, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Listing{}, false, nil
	}
	if err != nil {
		return model.Listing{}, false, fmt.Errorf("close listing: %w", err)
	}
	return mapListing(row), true, nil
}

// InsertPhotoUpload implements port.Tx.
func (t *postgresTx) InsertPhotoUpload(ctx context.Context, p model.PhotoUpload) (model.PhotoUpload, error) {
	row, err := t.q.InsertPhotoUpload(ctx, sharingstore.InsertPhotoUploadParams{
		ID: p.ID, MemberID: p.MemberID, ObjectKey: p.ObjectKey, ContentType: p.ContentType,
		SizeBytes: int32(p.SizeBytes), //nolint:gosec // validated ≤ MaxPhotoBytes
		ExpiresAt: p.ExpiresAt, CreatedAt: p.CreatedAt,
	})
	if err != nil {
		return model.PhotoUpload{}, fmt.Errorf("insert photo upload: %w", err)
	}
	return mapPhoto(row), nil
}

// GetPhotoUpload implements port.Tx.
func (t *postgresTx) GetPhotoUpload(ctx context.Context, id uuid.UUID, forUpdate bool) (model.PhotoUpload, error) {
	var row sharingstore.SharingPhotoUpload
	var err error
	if forUpdate {
		row, err = t.q.GetPhotoUploadForUpdate(ctx, id)
	} else {
		row, err = t.q.GetPhotoUpload(ctx, id)
	}
	if err != nil {
		return model.PhotoUpload{}, notFound(err, "get photo upload")
	}
	return mapPhoto(row), nil
}

// MarkPhotoUploadUsed implements port.Tx.
func (t *postgresTx) MarkPhotoUploadUsed(ctx context.Context, id, memberID uuid.UUID, usedAt, now time.Time) (model.PhotoUpload, bool, error) {
	row, err := t.q.MarkPhotoUploadUsed(ctx, sharingstore.MarkPhotoUploadUsedParams{
		UsedAt: &usedAt, ID: id, MemberID: memberID, Now: now,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.PhotoUpload{}, false, nil
	}
	if err != nil {
		return model.PhotoUpload{}, false, fmt.Errorf("mark photo used: %w", err)
	}
	return mapPhoto(row), true, nil
}

// MarkPhotoUploadProcessed implements port.Tx.
func (t *postgresTx) MarkPhotoUploadProcessed(ctx context.Context, id uuid.UUID, processedKey string) (model.PhotoUpload, bool, error) {
	row, err := t.q.MarkPhotoUploadProcessed(ctx, sharingstore.MarkPhotoUploadProcessedParams{
		ProcessedObjectKey: &processedKey, ID: id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.PhotoUpload{}, false, nil
	}
	if err != nil {
		return model.PhotoUpload{}, false, fmt.Errorf("mark photo processed: %w", err)
	}
	return mapPhoto(row), true, nil
}

// MarkPhotoUploadFailed implements port.Tx.
func (t *postgresTx) MarkPhotoUploadFailed(ctx context.Context, id uuid.UUID) (model.PhotoUpload, bool, error) {
	row, err := t.q.MarkPhotoUploadFailed(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.PhotoUpload{}, false, nil
	}
	if err != nil {
		return model.PhotoUpload{}, false, fmt.Errorf("mark photo failed: %w", err)
	}
	return mapPhoto(row), true, nil
}

// InsertContactRequest implements port.Tx.
func (t *postgresTx) InsertContactRequest(ctx context.Context, c model.ContactRequest) (model.ContactRequest, bool, error) {
	row, err := t.q.InsertContactRequest(ctx, sharingstore.InsertContactRequestParams{
		ID: c.ID, ListingID: c.ListingID, RequesterID: c.RequesterID, CreatedAt: c.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ContactRequest{}, false, nil
	}
	if err != nil {
		return model.ContactRequest{}, false, fmt.Errorf("insert contact request: %w", err)
	}
	return mapContact(row), true, nil
}

// GetContactRequestByListingRequester implements port.Tx.
func (t *postgresTx) GetContactRequestByListingRequester(ctx context.Context, listingID, requesterID uuid.UUID) (model.ContactRequest, error) {
	row, err := t.q.GetContactRequestByListingRequester(ctx, sharingstore.GetContactRequestByListingRequesterParams{
		ListingID: listingID, RequesterID: requesterID,
	})
	if err != nil {
		return model.ContactRequest{}, notFound(err, "get contact by listing")
	}
	return mapContact(row), nil
}

// GetContactRequest implements port.Tx.
func (t *postgresTx) GetContactRequest(ctx context.Context, id uuid.UUID, forUpdate bool) (model.ContactRequest, error) {
	var row sharingstore.SharingContactRequest
	var err error
	if forUpdate {
		row, err = t.q.GetContactRequestForUpdate(ctx, id)
	} else {
		row, err = t.q.GetContactRequest(ctx, id)
	}
	if err != nil {
		return model.ContactRequest{}, notFound(err, "get contact")
	}
	return mapContact(row), nil
}

// AcceptContactRequest implements port.Tx.
func (t *postgresTx) AcceptContactRequest(ctx context.Context, id uuid.UUID, at time.Time) (model.ContactRequest, bool, error) {
	row, err := t.q.AcceptContactRequest(ctx, sharingstore.AcceptContactRequestParams{AcceptedAt: &at, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ContactRequest{}, false, nil
	}
	if err != nil {
		return model.ContactRequest{}, false, fmt.Errorf("accept contact: %w", err)
	}
	return mapContact(row), true, nil
}

// Publish implements port.Tx.
func (t *postgresTx) Publish(ctx context.Context, now time.Time, e model.Event) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{Type: e.Type, Version: e.Version, Data: e.Data})
	return err
}

// MarkProcessed implements port.Tx.
func (t *postgresTx) MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (bool, error) {
	n, err := t.platform.MarkEventProcessed(ctx, platformstore.MarkEventProcessedParams{
		Consumer: consumer, EventID: eventID,
	})
	if err != nil {
		return false, fmt.Errorf("mark processed: %w", err)
	}
	return n == 1, nil
}

// ScheduleProcessPhoto implements port.Tx.
func (t *postgresTx) ScheduleProcessPhoto(ctx context.Context, listingID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind: model.TimerProcessPhoto, SubjectID: listingID,
		DedupKey: listingID.String(), DueAt: dueAt, Payload: map[string]string{},
	})
}

// ScheduleAutoClose implements port.Tx.
func (t *postgresTx) ScheduleAutoClose(ctx context.Context, listingID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind: model.TimerAutoClose, SubjectID: listingID,
		DedupKey: listingID.String(), DueAt: dueAt, Payload: map[string]string{},
	})
}

// CancelAutoClose implements port.Tx.
func (t *postgresTx) CancelAutoClose(ctx context.Context, listingID uuid.UUID, at time.Time) (int64, error) {
	return timers.Cancel(ctx, t.platform, at, model.TimerAutoClose, listingID)
}

func mapListing(r sharingstore.SharingListing) model.Listing {
	return model.Listing{
		ID: r.ID, AuthorID: r.AuthorID, IdempotencyKey: r.IdempotencyKey, Type: r.Type,
		Category: r.Category, Title: r.Title, Description: r.Description, DistrictID: r.DistrictID,
		PhotoUploadID: r.PhotoUploadID, PhotoObjectKey: r.PhotoObjectKey, Status: r.Status,
		RejectReason: r.RejectReason, ApprovedAt: r.ApprovedAt, ClosesAt: r.ClosesAt,
		ClosedAt: r.ClosedAt, CreatedAt: r.CreatedAt,
	}
}

func mapPhoto(r sharingstore.SharingPhotoUpload) model.PhotoUpload {
	return model.PhotoUpload{
		ID: r.ID, MemberID: r.MemberID, ObjectKey: r.ObjectKey, ContentType: r.ContentType,
		SizeBytes: int(r.SizeBytes), ExpiresAt: r.ExpiresAt, Status: r.Status,
		ProcessedObjectKey: r.ProcessedObjectKey, UsedAt: r.UsedAt, CreatedAt: r.CreatedAt,
	}
}

func mapContact(r sharingstore.SharingContactRequest) model.ContactRequest {
	return model.ContactRequest{
		ID: r.ID, ListingID: r.ListingID, RequesterID: r.RequesterID,
		Status: r.Status, CreatedAt: r.CreatedAt, AcceptedAt: r.AcceptedAt,
	}
}

var _ port.Store = (*PostgresStore)(nil)
var _ port.Tx = (*postgresTx)(nil)
