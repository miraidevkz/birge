// Package model holds notify domain types and invariants. No I/O.
package model

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Notification classes from the DeliveryRequested contract.
const (
	ClassCritical = "critical"
	ClassNormal   = "normal"
	ClassReminder = "reminder"
)

// Delivery channels.
const (
	ChannelPush = "push"
	ChannelSMS  = "sms"
)

// Delivery statuses.
const (
	StatusPending = "pending"
	StatusSent    = "sent"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// Recipient kinds stored on notifications.
const (
	// RecipientKindMember: ref is the member ID; inbox, push, critical SMS fallback.
	RecipientKindMember = "member"
	// RecipientKindExternal: ref is a trusted contact ID; SMS only, sent at once.
	RecipientKindExternal = "external"
	// RecipientKindStaff: ref is a duty shift ID (safety); SMS only, sent at once.
	// Kept apart from external so the phone is looked up in the right context.
	RecipientKindStaff = "staff"
)

// IsSMSOnly reports whether a recipient kind has no inbox and gets an
// immediate SMS instead.
func IsSMSOnly(kind string) bool {
	return kind == RecipientKindExternal || kind == RecipientKindStaff
}

// Device platforms.
const (
	PlatformIOS     = "ios"
	PlatformAndroid = "android"
)

// Staff roles accepted on DeliveryRequested.recipient.staff_role.
const (
	StaffModerator          = "moderator"
	StaffPsychologist       = "psychologist"
	StaffPsychologistBackup = "psychologist_backup"
)

// Platform timer kinds (ADR-0020).
const (
	TimerCriticalSMS = "notify.CriticalSMS"
	TimerPush        = "notify.Push"
	TimerExternalSMS = "notify.ExternalSMS"
)

// ConsumerDeliveryRequested is the durable consumer name for processed_events dedup.
const ConsumerDeliveryRequested = "notify.DeliveryRequested"

// SMSLinkPlaceholder in a configured SMS text (BIRGE_SMS_TEXTS) is replaced
// at send time with the link from port.SMSLinks. When no link can be made,
// an ordinary SMS is skipped (not found) or retried (transient failure); a
// critical SMS is never skipped or delayed for a link: it goes out at once
// with the text configured under template + SMSNoLinkSuffix. The rest of the
// text stays a neutral fixed sentence (ADR-0013).
const SMSLinkPlaceholder = "{link}"

// SMSNoLinkSuffix names the variant of a template's SMS text without the
// link, for example "emergency.sos_raised_contact.no_link": what a critical
// SMS says when its link cannot be made (the crisis path never waits for
// the link). If the variant is missing, the placeholder is cut out of the
// main text, so the SMS still goes out.
const SMSNoLinkSuffix = ".no_link"

// Sign-in code length bounds. Kratos generates 6 digits today; the range
// leaves room for a configuration change and refuses anything that is not a
// short run of digits, so no other text can ride in the code slot.
const (
	minSignInCodeLen = 4
	maxSignInCodeLen = 10
)

// ValidSignInCode reports whether code is 4 to 10 ASCII digits.
func ValidSignInCode(code string) bool {
	if len(code) < minSignInCodeLen || len(code) > maxSignInCodeLen {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

var (
	// ErrUnauthorized means the caller has no member session.
	ErrUnauthorized = errors.New("notify: unauthorized")
	// ErrNotFound means the notification, delivery, device or SMS link was not found.
	ErrNotFound = errors.New("notify: not found")
	// ErrInvalidInput means a request or event failed validation.
	ErrInvalidInput = errors.New("notify: invalid input")
	// ErrRecipientUnresolved means Recipients.Resolve returned no targets.
	ErrRecipientUnresolved = errors.New("notify: recipient unresolved")
	// ErrInvalidCursor means the inbox cursor could not be decoded.
	ErrInvalidCursor = errors.New("notify: invalid cursor")
)

var templateRe = regexp.MustCompile(`^[a-z]+\.[a-z_]+$`)

// ClassIsValid reports whether c is a known notification class.
func ClassIsValid(c string) bool {
	switch c {
	case ClassCritical, ClassNormal, ClassReminder:
		return true
	default:
		return false
	}
}

// RecipientKindIsValid reports whether k is a stored recipient kind.
func RecipientKindIsValid(k string) bool {
	return k == RecipientKindMember || k == RecipientKindExternal || k == RecipientKindStaff
}

// PlatformIsValid reports whether p is a known device platform.
func PlatformIsValid(p string) bool {
	return p == PlatformIOS || p == PlatformAndroid
}

// ValidateTemplate checks the locale key pattern from the contract.
func ValidateTemplate(t string) error {
	if !templateRe.MatchString(t) {
		return fmt.Errorf("%w: template", ErrInvalidInput)
	}
	return nil
}

// Recipient is exactly one of the three DeliveryRequested recipient forms.
type Recipient struct {
	MemberID         *uuid.UUID
	TrustedContactID *uuid.UUID
	StaffRole        string
	// StaffShiftID re-resolves a stored staff recipient; never accepted
	// from a DeliveryRequested event (which names a role, not a shift).
	StaffShiftID *uuid.UUID
}

// Validate ensures exactly one recipient form is set.
func (r Recipient) Validate() error {
	n := 0
	if r.MemberID != nil {
		n++
	}
	if r.TrustedContactID != nil {
		n++
	}
	if r.StaffRole != "" {
		n++
	}
	if r.StaffShiftID != nil {
		n++
	}
	if n != 1 {
		return fmt.Errorf("%w: recipient", ErrInvalidInput)
	}
	if r.StaffRole != "" {
		switch r.StaffRole {
		case StaffModerator, StaffPsychologist, StaffPsychologistBackup:
		default:
			return fmt.Errorf("%w: staff_role", ErrInvalidInput)
		}
	}
	return nil
}

// DeliveryRequest is the payload of notify.DeliveryRequested.v1.
type DeliveryRequest struct {
	RequestID uuid.UUID
	Recipient Recipient
	Class     string
	Template  string
	Params    map[string]string
}

// Validate checks contract-level invariants.
func (r DeliveryRequest) Validate() error {
	if r.RequestID == uuid.Nil {
		return fmt.Errorf("%w: request_id", ErrInvalidInput)
	}
	if err := r.Recipient.Validate(); err != nil {
		return err
	}
	if !ClassIsValid(r.Class) {
		return fmt.Errorf("%w: class", ErrInvalidInput)
	}
	if err := ValidateTemplate(r.Template); err != nil {
		return err
	}
	if len(r.Params) > 10 {
		return fmt.Errorf("%w: params", ErrInvalidInput)
	}
	for k, v := range r.Params {
		if k == "" || len(v) > 120 {
			return fmt.Errorf("%w: params", ErrInvalidInput)
		}
	}
	return nil
}

// ResolvedRecipient is one delivery target after resolving a Recipient form.
// Kind/Ref identify the target; Language selects the SMS text. Phones are
// looked up separately via Recipients.PhoneFor at send time and are never
// stored on this type or in the database.
type ResolvedRecipient struct {
	Kind     string // member | external | staff
	Ref      uuid.UUID
	Language string // "kk", "ru" or "en"
}

// Validate checks Kind and Ref.
func (r ResolvedRecipient) Validate() error {
	if !RecipientKindIsValid(r.Kind) {
		return fmt.Errorf("%w: recipient kind", ErrInvalidInput)
	}
	if r.Ref == uuid.Nil {
		return fmt.Errorf("%w: recipient ref", ErrInvalidInput)
	}
	return nil
}

// AsRecipient rebuilds the form needed to re-resolve language at send time.
func (r ResolvedRecipient) AsRecipient() Recipient {
	return RecipientForm(r.Kind, r.Ref)
}

// RecipientForm rebuilds the resolve form from a stored notification row.
func RecipientForm(kind string, ref uuid.UUID) Recipient {
	id := ref
	switch kind {
	case RecipientKindExternal:
		return Recipient{TrustedContactID: &id}
	case RecipientKindStaff:
		return Recipient{StaffShiftID: &id}
	}
	return Recipient{MemberID: &id}
}

// Notification is an inbox / delivery-tracking row.
type Notification struct {
	ID             uuid.UUID
	RequestID      uuid.UUID
	RecipientKind  string
	RecipientRef   uuid.UUID
	Template       string
	Params         map[string]string
	Class          string
	CreatedAt      time.Time
	ReadAt         *time.Time
	AcknowledgedAt *time.Time
}

// Device is a registered push endpoint.
type Device struct {
	MemberID  uuid.UUID
	DeviceID  uuid.UUID
	Platform  string
	PushToken string
	UpdatedAt time.Time
}

// InboxPage is a cursor page of notifications, newest first.
type InboxPage struct {
	Items      []Notification
	NextCursor string
}
