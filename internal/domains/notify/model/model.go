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
	RecipientKindMember   = "member"
	RecipientKindExternal = "external"
)

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

var (
	ErrUnauthorized        = errors.New("notify: unauthorized")
	ErrNotFound            = errors.New("notify: not found")
	ErrInvalidInput        = errors.New("notify: invalid input")
	ErrRecipientUnresolved = errors.New("notify: recipient unresolved")
	ErrInvalidCursor       = errors.New("notify: invalid cursor")
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
	return k == RecipientKindMember || k == RecipientKindExternal
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
// Kind/Ref identify the target; PhoneE164 and Language are used only when
// sending SMS and are never logged or stored.
type ResolvedRecipient struct {
	Kind      string // member | external
	Ref       uuid.UUID
	PhoneE164 string
	Language  string // "kk" or "ru"
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

// AsRecipient rebuilds the form needed to re-resolve phone/language at send time.
// Never stores or returns the phone number.
func (r ResolvedRecipient) AsRecipient() Recipient {
	ref := r.Ref
	if r.Kind == RecipientKindExternal {
		return Recipient{TrustedContactID: &ref}
	}
	return Recipient{MemberID: &ref}
}

// RecipientForm rebuilds the resolve form from a stored notification row.
func RecipientForm(kind string, ref uuid.UUID) Recipient {
	id := ref
	if kind == RecipientKindExternal {
		return Recipient{TrustedContactID: &id}
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
