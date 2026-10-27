// Package port defines the interfaces the directory service needs: its store
// (specialists, reviews, outbox, processed_events) and member profiles from
// membership (verification and first-name-only display names). Adapters for
// other contexts are wired later; tests use the fakes in directorytest.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/directory/model"
)

// MemberInfo reads member profiles owned by membership. Results are never
// logged, stored or put into events. DisplayName must be first name only.
type MemberInfo interface {
	Profiles(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]model.Profile, error)
}

// Store is the persistence port for directory tables and platform hooks.
type Store interface {
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction.
type Tx interface {
	InsertSpecialist(ctx context.Context, s model.Specialist) (created model.Specialist, inserted bool, err error)
	GetSpecialistByIdempotencyKey(ctx context.Context, addedBy, key uuid.UUID) (model.Specialist, error)
	GetSpecialist(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Specialist, error)
	SearchActiveSpecialists(ctx context.Context, f model.SearchFilter) ([]model.Specialist, error)
	ApproveSpecialist(ctx context.Context, id uuid.UUID) (s model.Specialist, updated bool, err error)
	RejectSpecialist(ctx context.Context, id uuid.UUID, reason string) (s model.Specialist, updated bool, err error)

	UpsertReview(ctx context.Context, r model.Review) (model.Review, error)
	GetReview(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Review, error)
	ListLatestActiveReviews(ctx context.Context, specialistID uuid.UUID, limit int32) ([]model.Review, error)
	AverageActiveRating(ctx context.Context, specialistID uuid.UUID) (model.RatingStats, error)
	ApproveReview(ctx context.Context, id uuid.UUID, at time.Time) (r model.Review, updated bool, err error)
	RejectReview(ctx context.Context, id uuid.UUID, reason string, at time.Time) (r model.Review, updated bool, err error)

	Publish(ctx context.Context, now time.Time, e model.Event) error
	MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (newly bool, err error)
}
