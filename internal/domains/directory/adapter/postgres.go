// Package adapter implements the directory ports on PostgreSQL (schema
// directory, sqlc in ../store) and the platform hooks it needs: outbox events
// and processed_events. Membership MemberInfo is wired later.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/directory/model"
	"birge/internal/domains/directory/port"
	directorystore "birge/internal/domains/directory/store"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
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
	return &postgresTx{q: directorystore.New(tx), platform: platformstore.New(tx)}
}

type postgresTx struct {
	q        *directorystore.Queries
	platform *platformstore.Queries
}

func notFound(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}

// InsertSpecialist implements port.Tx.
func (t *postgresTx) InsertSpecialist(ctx context.Context, s model.Specialist) (model.Specialist, bool, error) {
	row, err := t.q.InsertSpecialist(ctx, directorystore.InsertSpecialistParams{
		ID: s.ID, AddedBy: s.AddedBy, IdempotencyKey: s.IdempotencyKey, Name: s.Name,
		Profession: s.Profession, DistrictID: s.DistrictID, Needs: s.Needs, Contacts: s.Contacts,
		CreatedAt: s.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Specialist{}, false, nil
	}
	if err != nil {
		return model.Specialist{}, false, fmt.Errorf("insert specialist: %w", err)
	}
	return mapSpecialist(row), true, nil
}

// GetSpecialistByIdempotencyKey implements port.Tx.
func (t *postgresTx) GetSpecialistByIdempotencyKey(ctx context.Context, addedBy, key uuid.UUID) (model.Specialist, error) {
	row, err := t.q.GetSpecialistByIdempotencyKey(ctx, directorystore.GetSpecialistByIdempotencyKeyParams{
		AddedBy: addedBy, IdempotencyKey: key,
	})
	if err != nil {
		return model.Specialist{}, notFound(err, "get specialist by key")
	}
	return mapSpecialist(row), nil
}

// GetSpecialist implements port.Tx.
func (t *postgresTx) GetSpecialist(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Specialist, error) {
	var row directorystore.DirectorySpecialist
	var err error
	if forUpdate {
		row, err = t.q.GetSpecialistForUpdate(ctx, id)
	} else {
		row, err = t.q.GetSpecialist(ctx, id)
	}
	if err != nil {
		return model.Specialist{}, notFound(err, "get specialist")
	}
	return mapSpecialist(row), nil
}

// SearchActiveSpecialists implements port.Tx.
func (t *postgresTx) SearchActiveSpecialists(ctx context.Context, f model.SearchFilter) ([]model.Specialist, error) {
	rows, err := t.q.SearchActiveSpecialists(ctx, directorystore.SearchActiveSpecialistsParams{
		FilterProfession: f.Profession, FilterDistrict: f.DistrictID, FilterNeed: f.Need,
		CursorCreatedAt: f.CursorCreatedAt, CursorID: f.CursorID, PageLimit: f.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("search specialists: %w", err)
	}
	out := make([]model.Specialist, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapSpecialist(r))
	}
	return out, nil
}

// ApproveSpecialist implements port.Tx.
func (t *postgresTx) ApproveSpecialist(ctx context.Context, id uuid.UUID) (model.Specialist, bool, error) {
	row, err := t.q.ApproveSpecialist(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Specialist{}, false, nil
	}
	if err != nil {
		return model.Specialist{}, false, fmt.Errorf("approve specialist: %w", err)
	}
	return mapSpecialist(row), true, nil
}

// RejectSpecialist implements port.Tx.
func (t *postgresTx) RejectSpecialist(ctx context.Context, id uuid.UUID, reason string) (model.Specialist, bool, error) {
	row, err := t.q.RejectSpecialist(ctx, directorystore.RejectSpecialistParams{RejectReason: &reason, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Specialist{}, false, nil
	}
	if err != nil {
		return model.Specialist{}, false, fmt.Errorf("reject specialist: %w", err)
	}
	return mapSpecialist(row), true, nil
}

// UpsertReview implements port.Tx.
func (t *postgresTx) UpsertReview(ctx context.Context, r model.Review) (model.Review, error) {
	row, err := t.q.UpsertReview(ctx, directorystore.UpsertReviewParams{
		ID: r.ID, SpecialistID: r.SpecialistID, AuthorID: r.AuthorID,
		Rating: int32(r.Rating), //nolint:gosec // validated 1..5
		Text:   r.Text, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	})
	if err != nil {
		return model.Review{}, fmt.Errorf("upsert review: %w", err)
	}
	return mapReview(row), nil
}

// GetReview implements port.Tx.
func (t *postgresTx) GetReview(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Review, error) {
	var row directorystore.DirectoryReview
	var err error
	if forUpdate {
		row, err = t.q.GetReviewForUpdate(ctx, id)
	} else {
		row, err = t.q.GetReview(ctx, id)
	}
	if err != nil {
		return model.Review{}, notFound(err, "get review")
	}
	return mapReview(row), nil
}

// ListLatestActiveReviews implements port.Tx.
func (t *postgresTx) ListLatestActiveReviews(ctx context.Context, specialistID uuid.UUID, limit int32) ([]model.Review, error) {
	rows, err := t.q.ListLatestActiveReviews(ctx, directorystore.ListLatestActiveReviewsParams{
		SpecialistID: specialistID, PageLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	out := make([]model.Review, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapReview(r))
	}
	return out, nil
}

// AverageActiveRating implements port.Tx.
func (t *postgresTx) AverageActiveRating(ctx context.Context, specialistID uuid.UUID) (model.RatingStats, error) {
	row, err := t.q.AverageActiveRating(ctx, specialistID)
	if err != nil {
		return model.RatingStats{}, fmt.Errorf("average rating: %w", err)
	}
	return model.RatingStats{Average: row.Average, Count: int(row.ReviewCount)}, nil
}

// ApproveReview implements port.Tx.
func (t *postgresTx) ApproveReview(ctx context.Context, id uuid.UUID, at time.Time) (model.Review, bool, error) {
	row, err := t.q.ApproveReview(ctx, directorystore.ApproveReviewParams{UpdatedAt: at, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Review{}, false, nil
	}
	if err != nil {
		return model.Review{}, false, fmt.Errorf("approve review: %w", err)
	}
	return mapReview(row), true, nil
}

// RejectReview implements port.Tx.
func (t *postgresTx) RejectReview(ctx context.Context, id uuid.UUID, reason string, at time.Time) (model.Review, bool, error) {
	row, err := t.q.RejectReview(ctx, directorystore.RejectReviewParams{
		RejectReason: &reason, UpdatedAt: at, ID: id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Review{}, false, nil
	}
	if err != nil {
		return model.Review{}, false, fmt.Errorf("reject review: %w", err)
	}
	return mapReview(row), true, nil
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

func mapSpecialist(r directorystore.DirectorySpecialist) model.Specialist {
	needs := r.Needs
	if needs == nil {
		needs = []string{}
	}
	return model.Specialist{
		ID: r.ID, AddedBy: r.AddedBy, IdempotencyKey: r.IdempotencyKey, Name: r.Name,
		Profession: r.Profession, DistrictID: r.DistrictID, Needs: needs, Contacts: r.Contacts,
		Status: r.Status, RejectReason: r.RejectReason, CreatedAt: r.CreatedAt,
	}
}

func mapReview(r directorystore.DirectoryReview) model.Review {
	return model.Review{
		ID: r.ID, SpecialistID: r.SpecialistID, AuthorID: r.AuthorID, Rating: int(r.Rating),
		Text: r.Text, Status: r.Status, RejectReason: r.RejectReason,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

var _ port.Store = (*PostgresStore)(nil)
var _ port.Tx = (*postgresTx)(nil)
