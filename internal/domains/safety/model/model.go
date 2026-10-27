// Package model holds the safety domain types and invariants: escalations,
// duty shifts, staff roles, events and errors (ADR-0023). No I/O.
package model

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Escalation statuses (lifecycle open -> taken -> closed).
const (
	StatusOpen   = "open"
	StatusTaken  = "taken"
	StatusClosed = "closed"
)

// Escalation sources: where the member asked for a person or the crisis was detected.
const (
	SourceAssistant = "assistant"
	SourceCheckin   = "checkin"
	SourceSOS       = "sos"
	SourceMenu      = "menu"
)

// Escalation outcomes recorded on close.
const (
	OutcomeContacted         = "contacted"
	OutcomeReferred          = "referred"
	OutcomeEmergencyServices = "emergency_services"
	OutcomeNoAnswer          = "no_answer"
	OutcomeNotACrisis        = "not_a_crisis"
)

// Staff roles: the auth.Principal role names and the duty-shift roles.
const (
	RolePsychologist = "psychologist"
	RoleModerator    = "moderator"
	RoleAdmin        = "admin"
)

// Staff-role recipients of notify.DeliveryRequested resolved by ResolveStaffRole.
const (
	RecipientPsychologist       = "psychologist"
	RecipientPsychologistBackup = "psychologist_backup"
	RecipientModerator          = "moderator"
)

// Events published by safety, and the notify event it asks for.
const (
	EventEscalationOpened     = "safety.EscalationOpened"
	EventEscalationTaken      = "safety.EscalationTaken"
	EventEscalationClosed     = "safety.EscalationClosed"
	EventEscalationUnattended = "safety.EscalationUnattended"
	EventDeliveryRequested    = "notify.DeliveryRequested"
	EventVersion              = 1
)

// Notify templates (locale keys; the client and notify render them).
const (
	TemplateEscalationOpened     = "safety.escalation_opened"
	TemplateEscalationUnattended = "safety.escalation_unattended"
	ClassCritical                = "critical"
)

// TimerEscalationBackup fires when an escalation may still be open after the
// backup delay (ADR-0020); subject and dedup key are the escalation id.
const TimerEscalationBackup = "safety.EscalationBackup"

// Audit actions written to platform.audit_log.
const (
	AuditObjectEscalation = "safety.escalation"
	AuditObjectDutyShift  = "safety.duty_shift"
	AuditTake             = "escalation.take"
	AuditRevealContact    = "escalation.reveal_contact"
	AuditClose            = "escalation.close"
	AuditShiftAdd         = "duty_shift.add"
	AuditShiftCancel      = "duty_shift.cancel"
)

// Associated-data field names for sealed columns (crypto.AssociatedData).
const (
	FieldEscalationNote = "safety.escalations.note"
	FieldShiftPhone     = "safety.duty_shifts.phone"
)

// MaxNoteLen is the longest close note accepted, in bytes.
const MaxNoteLen = 4000

var (
	// ErrUnauthorized means there is no principal of the required kind.
	ErrUnauthorized = errors.New("safety: unauthorized")
	// ErrForbidden means the principal lacks the role or is not the staff
	// member who took the escalation.
	ErrForbidden = errors.New("safety: forbidden")
	// ErrNotFound means the escalation or shift does not exist.
	ErrNotFound = errors.New("safety: not found")
	// ErrInvalidInput means a request failed validation.
	ErrInvalidInput = errors.New("safety: invalid input")
	// ErrConflict means the escalation or shift is not in a state that allows
	// the operation (already taken, closed, shift started).
	ErrConflict = errors.New("safety: conflict")
	// ErrNoDutyCoverage means no shift of the requested role covers now.
	ErrNoDutyCoverage = errors.New("safety: no duty coverage")
)

var e164Re = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// SourceIsValid reports whether s is a known escalation source.
func SourceIsValid(s string) bool {
	switch s {
	case SourceAssistant, SourceCheckin, SourceSOS, SourceMenu:
		return true
	}
	return false
}

// StatusIsValid reports whether s is a known escalation status.
func StatusIsValid(s string) bool {
	return s == StatusOpen || s == StatusTaken || s == StatusClosed
}

// OutcomeIsValid reports whether o is a known close outcome.
func OutcomeIsValid(o string) bool {
	switch o {
	case OutcomeContacted, OutcomeReferred, OutcomeEmergencyServices, OutcomeNoAnswer, OutcomeNotACrisis:
		return true
	}
	return false
}

// ShiftRoleIsValid reports whether r is a role a duty shift can have.
func ShiftRoleIsValid(r string) bool { return r == RolePsychologist || r == RoleModerator }

// PhoneIsE164 reports whether p looks like an E.164 number.
func PhoneIsE164(p string) bool { return e164Re.MatchString(p) }

// Escalation is a request for a person. It holds no name or phone; Note is
// the sealed close note and is never decrypted by this context.
type Escalation struct {
	ID         uuid.UUID
	MemberID   uuid.UUID
	Source     string
	Status     string
	DistrictID *uuid.UUID
	OpenedAt   time.Time
	TakenBy    *uuid.UUID
	TakenAt    *time.Time
	Outcome    *string
	Note       []byte
	ClosedAt   *time.Time
}

// TakenByStaff reports whether staffID took the escalation.
func (e Escalation) TakenByStaff(staffID uuid.UUID) bool {
	return e.TakenBy != nil && *e.TakenBy == staffID
}

// EscalationFilter selects a page of the staff queue, newest first.
type EscalationFilter struct {
	Status         string // "" for any
	CursorOpenedAt *time.Time
	CursorID       *uuid.UUID
	Limit          int32
}

// DutyShift is one period during which a staff member is on duty. Phone is
// sealed with the staff member's data key; it is opened only to send an alert.
type DutyShift struct {
	ID          uuid.UUID
	StaffID     uuid.UUID
	Role        string
	StartsAt    time.Time
	EndsAt      time.Time
	Backup      bool
	Phone       []byte
	CreatedAt   time.Time
	CancelledAt *time.Time
}

// NewDutyShift is the validated input for adding a shift. Phone is plaintext
// here and must be sealed before it reaches the store.
type NewDutyShift struct {
	ID       uuid.UUID // the Idempotency-Key
	StaffID  uuid.UUID
	Role     string
	StartsAt time.Time
	EndsAt   time.Time
	Backup   bool
	Phone    string
}

// Contact is the member's contact shown to the staff member who took the
// escalation. Never logged, stored or put into events.
type Contact struct {
	DisplayName string
	Lang        string
	Phone       string
}

// ExternalRecipient is one on-duty shift resolved for notify: notify sends it
// an SMS directly. Ref is the shift id; PhoneE164 and Lang are used only to
// send and are never logged or stored by notify.
type ExternalRecipient struct {
	Ref       uuid.UUID
	PhoneE164 string
	Lang      string
}

// CrisisPhone is a crisis line from configuration, returned to the member.
type CrisisPhone struct {
	Number string
	Label  string
}

// AuditEntry is one platform.audit_log row. Reason is never free text from a
// user: it carries only fixed codes.
type AuditEntry struct {
	At         time.Time
	ActorID    uuid.UUID
	ActorRole  string
	Action     string
	ObjectType string
	ObjectID   uuid.UUID
	Reason     string
}

// EscalationOpenedData is the payload of safety.EscalationOpened v1.
type EscalationOpenedData struct {
	EscalationID uuid.UUID `json:"escalation_id"`
	MemberID     uuid.UUID `json:"member_id"`
	Source       string    `json:"source"`
}

// EscalationTakenData is the payload of safety.EscalationTaken v1.
type EscalationTakenData struct {
	EscalationID uuid.UUID `json:"escalation_id"`
	TakenBy      uuid.UUID `json:"taken_by"`
}

// EscalationClosedData is the payload of safety.EscalationClosed v1.
type EscalationClosedData struct {
	EscalationID uuid.UUID `json:"escalation_id"`
	Outcome      string    `json:"outcome"`
}

// EscalationUnattendedData is the payload of safety.EscalationUnattended v1.
type EscalationUnattendedData struct {
	EscalationID uuid.UUID `json:"escalation_id"`
	MinutesOpen  int       `json:"minutes_open"`
}

// DeliveryRecipient is the recipient object of notify.DeliveryRequested v1;
// safety only uses the staff_role form.
type DeliveryRecipient struct {
	StaffRole string `json:"staff_role"`
}

// DeliveryRequestedData is the payload of notify.DeliveryRequested v1. It
// carries no text or personal data: only the template key and the escalation id.
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
var deliveryNamespace = uuid.MustParse("6f1d3c9e-2b7a-4c55-9a0e-5d8b1f4e7a21")

// DeliveryRequestID is the request id for the alert template about an
// escalation. It is deterministic so a retried transaction or timer run
// produces the same id and notify's deduplication drops any duplicate.
func DeliveryRequestID(escalationID uuid.UUID, template string) uuid.UUID {
	return uuid.NewSHA1(deliveryNamespace, []byte(escalationID.String()+"|"+template))
}

// StaffAlert builds the critical notify.DeliveryRequested event for an escalation.
func StaffAlert(escalationID uuid.UUID, staffRole, template string) Event {
	return Event{
		Type:    EventDeliveryRequested,
		Version: EventVersion,
		Data: DeliveryRequestedData{
			RequestID: DeliveryRequestID(escalationID, template),
			Recipient: DeliveryRecipient{StaffRole: staffRole},
			Class:     ClassCritical,
			Template:  template,
			Params:    map[string]string{"escalation_id": escalationID.String()},
		},
	}
}
