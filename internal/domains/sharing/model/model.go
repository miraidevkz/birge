// Package model holds the sharing domain types and invariants: listings,
// photo uploads, contact requests, events and errors. No I/O.
//
// Listing titles and descriptions appear here for storage and API display;
// they never go into events, notifications or logs. Photos are item-only
// (ADR-0019); the server re-encodes them and strips metadata (ADR-0015).
package model

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Listing types (§ 4.5).
const (
	TypeGive = "give"
	TypeSwap = "swap"
	TypeNeed = "need"
)

// Listing statuses.
const (
	ListingPending  = "pending"
	ListingActive   = "active"
	ListingRejected = "rejected"
	ListingClosed   = "closed"
)

// Photo upload statuses.
const (
	PhotoPending   = "pending"
	PhotoProcessed = "processed"
	PhotoFailed    = "failed"
)

// Contact request statuses.
const (
	ContactPending  = "pending"
	ContactAccepted = "accepted"
)

// Content types accepted for item photos.
const (
	ContentJPEG = "image/jpeg"
	ContentPNG  = "image/png"
)

// Defaults and limits from § 4.5 / contract / ADR-0015.
const (
	DefaultListingTTL  = 30 * 24 * time.Hour
	PhotoUploadTTL     = 10 * time.Minute
	PhotoURLTTL        = 15 * time.Minute
	MaxPhotoBytes      = 5 * 1024 * 1024
	MaxPhotoDimension  = 1600
	FeedPageLimit      = 5
	MaxMyListings      = 100
	MaxTitleLen        = 80
	MinTitleLen        = 3
	MaxDescriptionLen  = 1000
	MaxRejectReasonLen = 40
	MaxCategoryLen     = 40
)

// Events published or consumed by sharing.
const (
	EventListingSubmitted  = "sharing.ListingSubmitted"
	EventContactAccepted   = "sharing.ContactAccepted"
	EventDeliveryRequested = "notify.DeliveryRequested"
	EventModerationDecided = "moderation.Decided"
	EventVersion           = 1
)

// Notify templates and delivery class.
const (
	TemplateContactRequest = "sharing.contact_request"
	TemplateListingDecided = "sharing.listing_decided"
	ClassNormal            = "normal"
)

// Platform timer kinds (ADR-0020).
const (
	// TimerProcessPhoto re-encodes a listing photo and strips metadata
	// (subject = listing id).
	TimerProcessPhoto = "sharing.ProcessPhoto"
	// TimerAutoClose closes an active listing after ListingTTL (subject = listing id).
	TimerAutoClose = "sharing.AutoClose"
)

// ConsumerModerationDecided is the processed_events consumer name for
// moderation.Decided (kind listing).
const ConsumerModerationDecided = "sharing.moderation.Decided"

// ModerationKindListing is the moderation.Decided kind this context applies.
const ModerationKindListing = "listing"

var (
	// ErrUnauthorized means there is no member session.
	ErrUnauthorized = errors.New("sharing: unauthorized")
	// ErrForbidden means the member may not do this (not verified, not author).
	ErrForbidden = errors.New("sharing: forbidden")
	// ErrNotFound means the object does not exist or is not visible to the caller.
	ErrNotFound = errors.New("sharing: not found")
	// ErrInvalidInput means a request failed validation.
	ErrInvalidInput = errors.New("sharing: invalid input")
	// ErrConflict means the current state does not allow the operation.
	ErrConflict = errors.New("sharing: conflict")
	// ErrPhoto means the uploaded bytes could not be decoded as an image.
	ErrPhoto = errors.New("sharing: photo")
	// ErrStorageNotConfigured means object storage is unset; photo uploads
	// answer 503 storage_not_configured.
	ErrStorageNotConfigured = errors.New("sharing: storage not configured")
)

var codeRe = regexp.MustCompile(`^[a-z_]{2,40}$`)

// TypeIsValid reports whether t is a known listing type.
func TypeIsValid(t string) bool {
	return t == TypeGive || t == TypeSwap || t == TypeNeed
}

// ContentTypeIsValid reports whether ct is an accepted photo content type.
func ContentTypeIsValid(ct string) bool {
	return ct == ContentJPEG || ct == ContentPNG
}

// CodeIsValid reports whether s matches the contract category/profession pattern.
func CodeIsValid(s string) bool {
	return codeRe.MatchString(s)
}

// UploadObjectKey is the raw object key for a photo upload slot.
func UploadObjectKey(uploadID uuid.UUID) string {
	return "sharing/uploads/" + uploadID.String()
}

// ProcessedObjectKey is the re-encoded JPEG key for a listing.
func ProcessedObjectKey(listingID uuid.UUID) string {
	return "sharing/listings/" + listingID.String() + ".jpg"
}

// Listing is one stored listing. PhotoObjectKey is set only after ProcessPhoto.
type Listing struct {
	ID             uuid.UUID
	AuthorID       uuid.UUID
	IdempotencyKey uuid.UUID
	Type           string
	Category       string
	Title          string
	Description    string
	DistrictID     uuid.UUID
	PhotoUploadID  *uuid.UUID
	PhotoObjectKey *string
	Status         string
	RejectReason   *string
	ApprovedAt     *time.Time
	ClosesAt       *time.Time
	ClosedAt       *time.Time
	CreatedAt      time.Time
}

// PhotoReady reports whether the listing may become active with respect to photos:
// no photo, or a processed object key is present.
func (l Listing) PhotoReady() bool {
	return l.PhotoUploadID == nil || l.PhotoObjectKey != nil
}

// ListingView is a listing for the API, with an optional presigned photo URL.
type ListingView struct {
	Listing
	PhotoURL string
	Mine     bool
}

// FeedFilter selects one page of the active listings feed.
type FeedFilter struct {
	ViewerID        uuid.UUID
	Type            *string
	Category        *string
	DistrictID      *uuid.UUID
	CursorCreatedAt *time.Time
	CursorID        *uuid.UUID
	Limit           int32
}

// FeedPage is one page of the listings feed.
type FeedPage struct {
	Items      []ListingView
	NextCursor string
}

// PhotoUpload is one upload slot for an item photo.
type PhotoUpload struct {
	ID                 uuid.UUID
	MemberID           uuid.UUID
	ObjectKey          string
	ContentType        string
	SizeBytes          int
	ExpiresAt          time.Time
	Status             string
	ProcessedObjectKey *string
	UsedAt             *time.Time
	CreatedAt          time.Time
}

// PhotoUploadSlot is returned to the client for a presigned PUT.
type PhotoUploadSlot struct {
	UploadID  uuid.UUID
	URL       string
	ExpiresAt time.Time
}

// ContactRequest is one "message the author" request.
type ContactRequest struct {
	ID          uuid.UUID
	ListingID   uuid.UUID
	RequesterID uuid.UUID
	Status      string
	CreatedAt   time.Time
	AcceptedAt  *time.Time
}

// NewListing is the validated input for creating a listing.
type NewListing struct {
	Type          string
	Category      string
	Title         string
	Description   string
	DistrictID    uuid.UUID
	PhotoUploadID *uuid.UUID
	AcceptRules   bool
}

// Profile is what sharing reads about a member from membership. Never logged.
type Profile struct {
	Verified bool
}

// ListingSubmittedData is the payload of sharing.ListingSubmitted v1.
type ListingSubmittedData struct {
	ListingID uuid.UUID `json:"listing_id"`
}

// ContactAcceptedData is the payload of sharing.ContactAccepted v1.
type ContactAcceptedData struct {
	ListingID   uuid.UUID `json:"listing_id"`
	AuthorID    uuid.UUID `json:"author_id"`
	RequesterID uuid.UUID `json:"requester_id"`
}

// ModerationDecidedData is the payload of moderation.Decided v1 (subset used here).
type ModerationDecidedData struct {
	ItemID      uuid.UUID `json:"item_id"`
	Kind        string    `json:"kind"`
	ObjectID    uuid.UUID `json:"object_id"`
	Approve     bool      `json:"approve"`
	Reason      string    `json:"reason,omitempty"`
	BlockAuthor bool      `json:"block_author"`
	LegalHold   bool      `json:"legal_hold"`
}

// DeliveryRecipient is the recipient object of notify.DeliveryRequested v1.
type DeliveryRecipient struct {
	MemberID *uuid.UUID `json:"member_id,omitempty"`
}

// DeliveryRequestedData is the payload of notify.DeliveryRequested v1.
// Template keys and IDs only — never titles, descriptions or names.
type DeliveryRequestedData struct {
	RequestID uuid.UUID         `json:"request_id"`
	Recipient DeliveryRecipient `json:"recipient"`
	Class     string            `json:"class"`
	Template  string            `json:"template"`
	Params    map[string]string `json:"params,omitempty"`
}

// Event is an outbox event to publish in the current transaction.
type Event struct {
	Type    string
	Version int
	Data    any
}

// deliveryNamespace derives deterministic DeliveryRequested request ids.
var deliveryNamespace = uuid.MustParse("b8f3d1a2-6c7e-4a9b-8d0f-1e2a3b4c5d6e")

// DeliveryRequestID is deterministic in the subject, template and recipient.
func DeliveryRequestID(subjectID uuid.UUID, template string, memberID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(deliveryNamespace, []byte(subjectID.String()+"|"+template+"|member:"+memberID.String()))
}

// Delivery builds a notify.DeliveryRequested for one member about subjectID.
func Delivery(subjectID, memberID uuid.UUID, template string, params map[string]string) Event {
	mid := memberID
	return Event{
		Type:    EventDeliveryRequested,
		Version: EventVersion,
		Data: DeliveryRequestedData{
			RequestID: DeliveryRequestID(subjectID, template, memberID),
			Recipient: DeliveryRecipient{MemberID: &mid},
			Class:     ClassNormal,
			Template:  template,
			Params:    params,
		},
	}
}
