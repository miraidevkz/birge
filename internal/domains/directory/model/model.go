// Package model holds the directory domain types and invariants: specialists,
// reviews, events and errors. No I/O. Specialist contacts are public work
// contacts, never a mother's personal data. Review author names are first
// name only, read from membership at display time.
package model

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Specialist / review statuses.
const (
	StatusPending  = "pending"
	StatusActive   = "active"
	StatusRejected = "rejected"
)

// Limits from § 4.5 / contract.
const (
	SearchPageLimit    = 20
	LatestReviewsLimit = 3
	MaxNameLen         = 120
	MinNameLen         = 2
	MaxContactsLen     = 300
	MaxReviewTextLen   = 1000
	MaxNeeds           = 10
	MaxRejectReasonLen = 40
)

// Events.
const (
	EventItemSubmitted     = "directory.ItemSubmitted"
	EventModerationDecided = "moderation.Decided"
	EventVersion           = 1
)

// Object types on directory.ItemSubmitted.
const (
	ObjectSpecialist = "specialist"
	ObjectReview     = "review"
)

// Moderation kinds this context applies.
const (
	ModerationKindSpecialist = "specialist"
	ModerationKindReview     = "review"
)

// ConsumerModerationDecided is the processed_events consumer name.
const ConsumerModerationDecided = "directory.moderation.Decided"

var (
	// ErrUnauthorized means there is no member session.
	ErrUnauthorized = errors.New("directory: unauthorized")
	// ErrForbidden means the member may not do this (not verified).
	ErrForbidden = errors.New("directory: forbidden")
	// ErrNotFound means the object does not exist or is not visible to the caller.
	ErrNotFound = errors.New("directory: not found")
	// ErrInvalidInput means a request failed validation.
	ErrInvalidInput = errors.New("directory: invalid input")
	// ErrConflict means the current state does not allow the operation.
	ErrConflict = errors.New("directory: conflict")
)

var codeRe = regexp.MustCompile(`^[a-z_]{2,40}$`)

// CodeIsValid reports whether s matches the contract code pattern.
func CodeIsValid(s string) bool { return codeRe.MatchString(s) }

// Specialist is one stored specialist card.
type Specialist struct {
	ID             uuid.UUID
	AddedBy        uuid.UUID
	IdempotencyKey uuid.UUID
	Name           string
	Profession     string
	DistrictID     uuid.UUID
	Needs          []string
	Contacts       *string
	Status         string
	RejectReason   *string
	CreatedAt      time.Time
}

// Review is one stored review.
type Review struct {
	ID           uuid.UUID
	SpecialistID uuid.UUID
	AuthorID     uuid.UUID
	Rating       int
	Text         string
	Status       string
	RejectReason *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ReviewView is a review for the API with first-name-only author.
type ReviewView struct {
	Review
	AuthorName string
}

// SpecialistView is a specialist card for the API.
type SpecialistView struct {
	Specialist
	AverageRating *float32
	ReviewCount   int
	LatestReviews []ReviewView
}

// SearchFilter selects one page of active specialists.
type SearchFilter struct {
	Profession      *string
	DistrictID      *uuid.UUID
	Need            *string
	CursorCreatedAt *time.Time
	CursorID        *uuid.UUID
	Limit           int32
}

// SearchPage is one page of specialist search results.
type SearchPage struct {
	Items      []SpecialistView
	NextCursor string
}

// NewSpecialist is validated input for suggesting a specialist.
type NewSpecialist struct {
	Name       string
	Profession string
	DistrictID uuid.UUID
	Needs      []string
	Contacts   *string
}

// NewReview is validated input for putting a review.
type NewReview struct {
	Rating int
	Text   string
}

// Profile is what directory reads about a member. Never logged.
type Profile struct {
	Verified    bool
	DisplayName string // first name only
}

// ItemSubmittedData is the payload of directory.ItemSubmitted v1.
type ItemSubmittedData struct {
	ObjectType string    `json:"object_type"`
	ObjectID   uuid.UUID `json:"object_id"`
}

// ModerationDecidedData is the payload of moderation.Decided v1.
type ModerationDecidedData struct {
	ItemID      uuid.UUID `json:"item_id"`
	Kind        string    `json:"kind"`
	ObjectID    uuid.UUID `json:"object_id"`
	Approve     bool      `json:"approve"`
	Reason      string    `json:"reason,omitempty"`
	BlockAuthor bool      `json:"block_author"`
	LegalHold   bool      `json:"legal_hold"`
}

// Event is an outbox event to publish in the current transaction.
type Event struct {
	Type    string
	Version int
	Data    any
}

// RatingStats is the average and count of active reviews.
type RatingStats struct {
	Average float64
	Count   int
}
