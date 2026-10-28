// Package model holds the emergency domain types and invariants: SOS
// requests, responses, the emergency plan (child card and trusted contacts),
// events, notify requests and errors. No I/O.
//
// Personal data (SOS text, child card, external contacts' names and phones)
// appears here only as sealed bytes or as plaintext on its way to the sealer
// or to the screen; it never goes into events, logs or errors.
package model

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// SOS types. "I feel very bad" is not an SOS type: it is safety's
// requestHumanHelp with source sos.
const (
	SosChildcare      = "childcare"
	SosItemOrMedicine = "item_or_medicine"
	SosOther          = "other"
)

// SOS statuses.
const (
	SosOpen   = "open"
	SosClosed = "closed"
)

// Close reasons recorded on a closed SOS.
const (
	CloseByAuthor = "author"
	CloseAuto     = "auto"
)

// Trusted contact kinds and statuses.
const (
	ContactMember    = "member"
	ContactExternal  = "external"
	ContactInvited   = "invited"
	ContactConfirmed = "confirmed"
)

// DefaultLang is the language used when a member's language is unknown.
const DefaultLang = "ru"

// Limits.
const (
	// MaxSosTextRunes is the longest SOS text, in characters (§ 4.6).
	MaxSosTextRunes = 300
	// MaxChildCardRunes is the longest child card, in characters (contract).
	MaxChildCardRunes = 2000
	// MaxContactNameRunes is the longest external contact name, in characters.
	MaxContactNameRunes = 60
	// MaxSosPerHour is the SOS rate limit per member (§ 4.6).
	MaxSosPerHour = 3
	// MaxTrustedContacts is the most active (invited + confirmed) contacts per member (§ 4.6).
	MaxTrustedContacts = 3
	// MaxPlanSendsPerHour bounds plan sends, which text external contacts.
	MaxPlanSendsPerHour = 3
	// MySosLimit is how many of her SOS requests a member sees (contract).
	MySosLimit = 20
	// FeedPageLimit is the district feed page size (contract maximum 50).
	FeedPageLimit = 50
	// ResponsesLimit is the most responses listed for one SOS (contract).
	ResponsesLimit = 100
)

// Events published by emergency, and the notify event it asks for.
const (
	EventSosRaised           = "emergency.SosRaised"
	EventSosShared           = "emergency.SosShared"
	EventSosClosed           = "emergency.SosClosed"
	EventSosResponseAccepted = "emergency.SosResponseAccepted"
	EventPlanSent            = "emergency.PlanSent"
	EventDeliveryRequested   = "notify.DeliveryRequested"
	EventVersion             = 1
)

// Notify templates (locale keys; the client and notify render them) and
// delivery classes.
const (
	// TemplateSosRaised alerts the moderators about a new SOS.
	TemplateSosRaised = "emergency.sos_raised"
	// TemplateSosRaisedContact tells a confirmed trusted contact that the
	// member raised an SOS (external contacts: SMS without content).
	TemplateSosRaisedContact = "emergency.sos_raised_contact"
	// TemplateSosResponded tells the author that someone pressed "I can help".
	TemplateSosResponded = "emergency.sos_responded"
	// TemplateSosShared is the district push when an SOS is shared to the feed.
	TemplateSosShared = "emergency.sos_shared"
	// TemplateTrustedInvitation asks a member to accept being a trusted contact.
	TemplateTrustedInvitation = "emergency.trusted_invitation"
	// TemplatePlanSent tells a confirmed trusted contact that a plan was sent.
	TemplatePlanSent = "emergency.plan_sent"

	ClassCritical = "critical"
	ClassNormal   = "normal"

	// StaffModerator is the notify staff_role that receives SOS alerts.
	StaffModerator = "moderator"
)

// TimerSosAutoClose closes an SOS that is still open after the auto-close
// delay (ADR-0020); subject and dedup key are the SOS id.
const TimerSosAutoClose = "emergency.SosAutoClose"

// TimerPlanLinkPurge deletes an external plan link PlanLinkRetention after it
// expired (ADR-0023); subject and dedup key are the link id.
const TimerPlanLinkPurge = "emergency.PlanLinkPurge"

// External plan links (viewExternalPlan).
const (
	// PlanLinkTTL is how long a link works after the plan send (contract: 24 hours).
	PlanLinkTTL = 24 * time.Hour
	// PlanLinkRetention is how long an expired link row is kept before the
	// purge timer deletes it (ADR-0023).
	PlanLinkRetention = 7 * 24 * time.Hour
	// PlanLinkTokenBytes is the token's entropy: 32 random bytes, 43
	// characters of unpadded base64url.
	PlanLinkTokenBytes = 32
)

// Audit codes written to platform.audit_log.
const (
	AuditActorMember     = "member"
	AuditObjectChildCard = "emergency.child_card"
	AuditViewSharedPlan  = "emergency_plan.view_shared"
	// AuditActorExternalContact is the actor role of a view through an SMS
	// link: the actor id is the trusted contact id (she has no account).
	AuditActorExternalContact = "external_contact"
	AuditViewExternalPlan     = "emergency_plan.view_external"
)

// Associated-data field names for sealed columns (crypto.AssociatedData).
// They are part of the schema: changing one makes stored ciphertexts unreadable.
const (
	FieldSosText      = "emergency.sos.text"
	FieldChildCard    = "emergency.child_cards.text"
	FieldContactName  = "emergency.trusted_contacts.name"
	FieldContactPhone = "emergency.trusted_contacts.phone"
)

// Lock scopes for Tx.Lock: per-member serialisation of limit checks.
const (
	LockSos      = "emergency.sos"
	LockContacts = "emergency.trusted_contacts"
	LockPlan     = "emergency.plan"
	// LockPlanLink serialises link minting per trusted contact (key = contact id).
	LockPlanLink = "emergency.plan_link"
)

var (
	// ErrUnauthorized means there is no member session.
	ErrUnauthorized = errors.New("emergency: unauthorized")
	// ErrForbidden means the member may not do this (not verified, not met
	// in person, not a confirmed contact who was sent the plan).
	ErrForbidden = errors.New("emergency: forbidden")
	// ErrNotFound means the object does not exist or is not visible to the
	// caller (another member's SOS, a blocked author, a removed contact).
	ErrNotFound = errors.New("emergency: not found")
	// ErrInvalidInput means a request failed validation.
	ErrInvalidInput = errors.New("emergency: invalid input")
	// ErrConflict means the current state does not allow the operation
	// (childcare share, closed SOS, contact limit, nothing to send).
	ErrConflict = errors.New("emergency: conflict")
	// ErrRateLimited means the member hit a per-hour limit.
	ErrRateLimited = errors.New("emergency: rate limited")
)

var e164Re = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// SosTypeIsValid reports whether t is a known SOS type.
func SosTypeIsValid(t string) bool {
	return t == SosChildcare || t == SosItemOrMedicine || t == SosOther
}

// Shareable reports whether an SOS of type t may ever reach the district
// feed. Childcare never may (N5: only trusted contacts and moderators).
func Shareable(t string) bool { return t != SosChildcare }

// ValidateSosText checks the SOS text: 1..300 characters, not blank.
func ValidateSosText(text string) error {
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > MaxSosTextRunes {
		return ErrInvalidInput
	}
	return nil
}

// ValidateChildCard checks the child card text: at most 2000 characters.
func ValidateChildCard(text string) error {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > MaxChildCardRunes {
		return ErrInvalidInput
	}
	return nil
}

// ValidateContactName checks an external contact's name: 1..60 characters.
func ValidateContactName(name string) error {
	if !utf8.ValidString(name) || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > MaxContactNameRunes {
		return ErrInvalidInput
	}
	return nil
}

// PhoneIsE164 reports whether p looks like an E.164 number.
func PhoneIsE164(p string) bool { return e164Re.MatchString(p) }

// MaskPhone returns an E.164 number for display with only the country's
// first digit and the last four digits visible: +70000000001 -> "+7 *** *** 00 01".
// It is computed for a response and never stored or logged.
func MaskPhone(p string) string {
	if !PhoneIsE164(p) {
		return "***"
	}
	d := p[1:]
	last := d[len(d)-4:]
	return "+" + d[:1] + " *** *** " + last[:2] + " " + last[2:]
}

// LangOrDefault returns lang when it is a supported language, else "ru".
func LangOrDefault(lang string) string {
	if lang == "kk" || lang == "ru" {
		return lang
	}
	return DefaultLang
}

// Sos is one SOS request. Text is sealed with the author's key (AD
// FieldSosText|<id>); it is opened only for display to the author or to the
// district feed.
type Sos struct {
	ID             uuid.UUID
	MemberID       uuid.UUID
	IdempotencyKey uuid.UUID
	Type           string
	Text           []byte
	Status         string
	CreatedAt      time.Time
	ClosesAt       time.Time
	DistrictID     *uuid.UUID
	SharedAt       *time.Time
	ClosedAt       *time.Time
	CloseReason    *string
	// ResponseCount is filled by the queries that count responses.
	ResponseCount int
}

// Shared reports whether the SOS was shared to the district feed.
func (s Sos) Shared() bool { return s.SharedAt != nil }

// SosView is an SOS of the caller with its text opened, for the API.
type SosView struct {
	Sos
	PlainText string
}

// FeedFilter selects one page of a district feed, newest first.
type FeedFilter struct {
	ViewerID        uuid.UUID
	DistrictID      uuid.UUID
	Now             time.Time
	CursorCreatedAt *time.Time
	CursorID        *uuid.UUID
	Limit           int32
}

// FeedRow is one stored row of the district feed (text still sealed).
type FeedRow struct {
	ID            uuid.UUID
	AuthorID      uuid.UUID
	Type          string
	Text          []byte
	CreatedAt     time.Time
	RespondedByMe bool
}

// FeedItem is one item shown in the district feed.
type FeedItem struct {
	ID            uuid.UUID
	Type          string
	Text          string
	DisplayName   string
	CreatedAt     time.Time
	RespondedByMe bool
}

// FeedPage is one page of the district feed.
type FeedPage struct {
	Items      []FeedItem
	NextCursor string
}

// SosResponse is one "I can help".
type SosResponse struct {
	ID          uuid.UUID
	SosID       uuid.UUID
	ResponderID uuid.UUID
	CreatedAt   time.Time
	AcceptedAt  *time.Time
}

// ResponseView is a response shown to the SOS author.
type ResponseView struct {
	SosResponse
	DisplayName string
	MetInPerson bool
}

// ChildCard is a member's sealed child card (AD FieldChildCard|<member id>).
type ChildCard struct {
	MemberID   uuid.UUID
	Text       []byte
	UpdatedAt  time.Time
	LastSentAt *time.Time
}

// TrustedContact is one entry of a member's emergency plan. For kind
// external, Name and Phone are sealed with the owner's key (AD
// FieldContactName|<id>, FieldContactPhone|<id>); for kind member they are nil.
type TrustedContact struct {
	ID              uuid.UUID
	OwnerID         uuid.UUID
	IdempotencyKey  uuid.UUID
	Kind            string
	ContactMemberID *uuid.UUID
	Name            []byte
	Phone           []byte
	Lang            string
	Status          string
	CreatedAt       time.Time
	ConfirmedAt     *time.Time
	PlanSentAt      *time.Time
	RemovedAt       *time.Time
}

// Active reports whether the contact has not been removed.
func (c TrustedContact) Active() bool { return c.RemovedAt == nil }

// Confirmed reports whether the contact is confirmed and active.
func (c TrustedContact) Confirmed() bool { return c.Active() && c.Status == ContactConfirmed }

// NewTrustedContact is the validated input for adding a contact. Name and
// Phone are plaintext here and must be sealed before they reach the store.
type NewTrustedContact struct {
	Kind     string
	MemberID uuid.UUID // kind member
	Name     string    // kind external
	Phone    string    // kind external
}

// ContactView is a trusted contact for display: the display name, and for
// external contacts the masked phone (never the full number).
type ContactView struct {
	ID          uuid.UUID
	Kind        string
	Status      string
	DisplayName string
	PhoneMasked string
}

// Plan is the caller's emergency plan for display.
type Plan struct {
	ChildCard  string
	Contacts   []ContactView
	LastSentAt *time.Time
}

// SharedPlan is another member's plan as her confirmed trusted contact sees it.
type SharedPlan struct {
	OwnerDisplayName string
	ChildCard        string
	SentAt           time.Time
}

// PlanLink is one stored external plan link. TokenHash is
// HMAC-SHA256(plan link key, token); the token itself is never stored.
type PlanLink struct {
	ID        uuid.UUID
	TokenHash []byte
	ContactID uuid.UUID
	OwnerID   uuid.UUID
	// SentAt is the plan send the link belongs to; ExpiresAt = SentAt + PlanLinkTTL.
	SentAt    time.Time
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// PlanSend records one "send the plan" confirmation.
type PlanSend struct {
	ID             uuid.UUID
	MemberID       uuid.UUID
	IdempotencyKey uuid.UUID
	ContactCount   int
	SentAt         time.Time
}

// Profile is what emergency reads about a member from membership. Never
// logged, stored or put into events.
type Profile struct {
	Verified    bool
	DistrictID  *uuid.UUID
	DisplayName string
	Lang        string
}

// AuditEntry is one platform.audit_log row. Reason carries fixed codes only.
type AuditEntry struct {
	At         time.Time
	ActorID    uuid.UUID
	ActorRole  string
	Action     string
	ObjectType string
	ObjectID   uuid.UUID
	Reason     string
}

// SosRaisedData is the payload of emergency.SosRaised v1.
type SosRaisedData struct {
	SosID    uuid.UUID `json:"sos_id"`
	MemberID uuid.UUID `json:"member_id"`
	Type     string    `json:"type"`
}

// SosSharedData is the payload of emergency.SosShared v1.
type SosSharedData struct {
	SosID      uuid.UUID `json:"sos_id"`
	DistrictID uuid.UUID `json:"district_id"`
}

// SosClosedData is the payload of emergency.SosClosed v1.
type SosClosedData struct {
	SosID uuid.UUID `json:"sos_id"`
}

// SosResponseAcceptedData is the payload of emergency.SosResponseAccepted v1.
type SosResponseAcceptedData struct {
	SosID       uuid.UUID `json:"sos_id"`
	AuthorID    uuid.UUID `json:"author_id"`
	ResponderID uuid.UUID `json:"responder_id"`
}

// PlanSentData is the payload of emergency.PlanSent v1.
type PlanSentData struct {
	MemberID uuid.UUID `json:"member_id"`
}

// DeliveryRecipient is the recipient object of notify.DeliveryRequested v1:
// exactly one field is set.
type DeliveryRecipient struct {
	MemberID         *uuid.UUID `json:"member_id,omitempty"`
	TrustedContactID *uuid.UUID `json:"trusted_contact_id,omitempty"`
	StaffRole        string     `json:"staff_role,omitempty"`
}

// DeliveryRequestedData is the payload of notify.DeliveryRequested v1. It
// carries template keys and IDs only, never the SOS text, a name or a phone.
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
var deliveryNamespace = uuid.MustParse("3c0b8e57-91d4-4a2f-b6e8-7f25a1c94d03")

// DeliveryRequestID is the request id of one delivery: deterministic in the
// subject (SOS, response, contact or plan send), the template and the
// recipient, so a retried transaction yields the same id and notify drops
// the duplicate.
func DeliveryRequestID(subjectID uuid.UUID, template string, recipient DeliveryRecipient) uuid.UUID {
	ref := recipient.StaffRole
	switch {
	case recipient.MemberID != nil:
		ref = "member:" + recipient.MemberID.String()
	case recipient.TrustedContactID != nil:
		ref = "trusted_contact:" + recipient.TrustedContactID.String()
	}
	return uuid.NewSHA1(deliveryNamespace, []byte(subjectID.String()+"|"+template+"|"+ref))
}

// Delivery builds a notify.DeliveryRequested event about subjectID.
func Delivery(subjectID uuid.UUID, recipient DeliveryRecipient, class, template string, params map[string]string) Event {
	return Event{
		Type:    EventDeliveryRequested,
		Version: EventVersion,
		Data: DeliveryRequestedData{
			RequestID: DeliveryRequestID(subjectID, template, recipient),
			Recipient: recipient,
			Class:     class,
			Template:  template,
			Params:    params,
		},
	}
}

// ContactRecipient is the notify recipient for a confirmed trusted contact:
// member_id for a member contact, trusted_contact_id for an external one
// (notify resolves the phone at send time through ContactPhone).
func ContactRecipient(c TrustedContact) DeliveryRecipient {
	if c.Kind == ContactMember && c.ContactMemberID != nil {
		id := *c.ContactMemberID
		return DeliveryRecipient{MemberID: &id}
	}
	id := c.ID
	return DeliveryRecipient{TrustedContactID: &id}
}

// MemberRecipient is the notify recipient for one member.
func MemberRecipient(memberID uuid.UUID) DeliveryRecipient {
	id := memberID
	return DeliveryRecipient{MemberID: &id}
}
