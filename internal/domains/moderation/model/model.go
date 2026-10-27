// Package model holds the moderation domain types, the decision validation
// matrix and the event payloads. It has no I/O.
//
// Moderation owns queue items and decisions, never the content under review:
// summaries come from the owning context through port.ContentSource and are
// never stored, logged or put into events (ADR-0007, AGENTS.md security
// rules).
package model

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"

	"birge/internal/platform/staffauth"
)

// Errors returned by the service; the HTTP layer maps them to problems.
var (
	ErrUnauthorized = errors.New("moderation: unauthorized")
	ErrForbidden    = errors.New("moderation: forbidden")
	ErrNotFound     = errors.New("moderation: not found")
	ErrInvalidInput = errors.New("moderation: invalid input")
	// ErrAlreadyDecided means the item was decided before; a decision is final.
	ErrAlreadyDecided = errors.New("moderation: already decided")
)

// Kind is what an item reviews; it names the owning context's object.
type Kind string

// Item kinds (contract ModerationKind). For KindReport and KindChatMessage
// the object is a chat message.
const (
	KindListing      Kind = "listing"
	KindSpecialist   Kind = "specialist"
	KindReview       Kind = "review"
	KindReport       Kind = "report"
	KindChatMessage  Kind = "chat_message"
	KindVerification Kind = "verification"
)

// Kinds lists every kind.
var Kinds = []Kind{KindListing, KindSpecialist, KindReview, KindReport, KindChatMessage, KindVerification}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return slices.Contains(Kinds, k) }

// AllowsAuthorActions reports whether a decision on k may block the author or
// put the content under legal hold: only chat content (ADR-0016, ADR-0019).
func (k Kind) AllowsAuthorActions() bool { return k == KindReport || k == KindChatMessage }

// Item statuses.
const (
	StatusOpen    = "open"
	StatusDecided = "decided"
)

// Reasons a rejection may carry (contract ModerationDecision.reason). The code
// goes to the author through the owning context.
var Reasons = []string{
	"rules_medicine", "rules_money", "person_in_photo", "unsafe",
	"spam", "inaccurate", "duplicate", "other",
}

// ReasonIsValid reports whether r is a known rejection reason.
func ReasonIsValid(r string) bool { return slices.Contains(Reasons, r) }

// sourceReasonRe is the shape of a hold or report reason code taken from an
// event; anything else is dropped rather than stored (it might be prose).
var sourceReasonRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// SourceReason returns r when it is a short code, otherwise "".
func SourceReason(r string) string {
	if sourceReasonRe.MatchString(r) {
		return r
	}
	return ""
}

// Paging.
const (
	PageSize      = 50
	MaxSummaryLen = 2000 // contract ModerationItem.summary maxLength, in runes
	MaxCursorLen  = 128
)

// Item is one queue item. It carries no content.
type Item struct {
	ID           uuid.UUID
	Kind         Kind
	ObjectID     uuid.UUID
	Status       string
	SourceReason string
	ReportCount  int
	CreatedAt    time.Time
	DecidedBy    uuid.UUID
	DecidedAt    time.Time
	Decision     Decision
}

// Submission is a request to review an object, built from a consumed event.
type Submission struct {
	Kind         Kind
	ObjectID     uuid.UUID
	SourceReason string
}

// Decision is a moderator's decision on one item. Approve keeps or publishes
// the object; for a report, approve keeps the message (the report is
// dismissed) and reject takes it down.
type Decision struct {
	Approve     bool
	Reason      string
	BlockAuthor bool
	LegalHold   bool
}

// ValidateDecision checks d against the item kind:
//   - a rejection needs a known reason; an approval carries none;
//   - block author and legal hold only for report and chat_message, and only
//     together with a rejection (ADR-0019: the content is hidden).
func ValidateDecision(k Kind, d Decision) error {
	if !k.Valid() {
		return fmt.Errorf("%w: kind", ErrInvalidInput)
	}
	if d.Approve {
		if d.Reason != "" {
			return fmt.Errorf("%w: reason only when rejecting", ErrInvalidInput)
		}
	} else if !ReasonIsValid(d.Reason) {
		return fmt.Errorf("%w: reason required when rejecting", ErrInvalidInput)
	}
	if d.BlockAuthor || d.LegalHold {
		if !k.AllowsAuthorActions() {
			return fmt.Errorf("%w: blockAuthor and legalHold only for report and chat_message", ErrInvalidInput)
		}
		if d.Approve {
			return fmt.Errorf("%w: blockAuthor and legalHold only when rejecting", ErrInvalidInput)
		}
	}
	return nil
}

// StaffRole returns the role a staff principal acts in for moderation:
// admin or moderator, or "" when none of its roles may moderate. Every
// moderation action (queue, decisions incl. verification, block author,
// legal hold) needs one of these two roles.
func StaffRole(roles []string) string {
	switch {
	case slices.Contains(roles, staffauth.RoleAdmin):
		return staffauth.RoleAdmin
	case slices.Contains(roles, staffauth.RoleModerator):
		return staffauth.RoleModerator
	}
	return ""
}

// Summary is the content under review as the owning context shows it to
// staff: no contact data, never stored or logged by moderation.
type Summary struct {
	Text     string
	PhotoURL string
}

// QueueItem is an item with its summary, for the staff queue.
type QueueItem struct {
	Item    Item
	Summary Summary
}

// QueuePage is one page of the queue.
type QueuePage struct {
	Items      []QueueItem
	NextCursor string
}

// Event types consumed and produced.
const (
	EventListingSubmitted      = "sharing.ListingSubmitted"
	EventItemSubmitted         = "directory.ItemSubmitted"
	EventMessageHeld           = "chat.MessageHeld"
	EventMessageReported       = "chat.MessageReported"
	EventVerificationRequested = "membership.VerificationRequested"
	EventDecided               = "moderation.Decided"
)

// ConsumerQueue is the platform.processed_events consumer name of the queue.
// Event IDs are UUIDv7 and unique across types, so one name covers all five.
const ConsumerQueue = "moderation.Queue"

// Audit codes (platform.audit_log).
const (
	AuditObjectItem      = "moderation.item"
	AuditActionApprove   = "moderation.approve"
	AuditActionReject    = "moderation.reject"
	AuditActionBlock     = "moderation.block_author"
	AuditActionLegalHold = "moderation.legal_hold"
)

// Event is a domain event to publish through the outbox.
type Event struct {
	Type    string
	Version int
	Data    any
}

// DecidedData is the payload of moderation.Decided v1.
type DecidedData struct {
	ItemID      uuid.UUID `json:"item_id"`
	Kind        Kind      `json:"kind"`
	ObjectID    uuid.UUID `json:"object_id"`
	Approve     bool      `json:"approve"`
	Reason      string    `json:"reason,omitempty"`
	BlockAuthor bool      `json:"block_author"`
	LegalHold   bool      `json:"legal_hold"`
}

// ListingSubmittedData is the payload of sharing.ListingSubmitted v1.
type ListingSubmittedData struct {
	ListingID uuid.UUID `json:"listing_id"`
}

// ItemSubmittedData is the payload of directory.ItemSubmitted v1.
type ItemSubmittedData struct {
	ObjectType string    `json:"object_type"`
	ObjectID   uuid.UUID `json:"object_id"`
}

// MessageHeldData is the payload of chat.MessageHeld v1.
type MessageHeldData struct {
	MessageID uuid.UUID `json:"message_id"`
	RoomID    uuid.UUID `json:"room_id"`
	Reason    string    `json:"reason,omitempty"`
}

// MessageReportedData is the payload of chat.MessageReported v1.
type MessageReportedData struct {
	MessageID uuid.UUID `json:"message_id"`
	RoomID    uuid.UUID `json:"room_id"`
	Reason    string    `json:"reason,omitempty"`
}

// VerificationRequestedData is the payload of membership.VerificationRequested v1.
type VerificationRequestedData struct {
	MemberID  uuid.UUID `json:"member_id"`
	RequestID uuid.UUID `json:"request_id"`
}
