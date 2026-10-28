// Package model holds the chat domain types and invariants: rooms, room
// membership, messages and their visibility, moderation permissions, the
// content filters, events, notify requests, fan-out notices and errors. No I/O.
//
// Message text appears here only as sealed bytes or as plaintext on its way
// to the sealer or to the reader's screen; it never goes into events,
// fan-out notices, logs or errors.
package model

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Room kinds.
const (
	KindDistrict = "district"
	KindTopic    = "topic"
	KindDirect   = "direct"
)

// Direct room origins: the event that opened the room.
const (
	OriginMatch   = "circle.match"
	OriginSos     = "emergency.sos"
	OriginListing = "sharing.listing"
)

// Stored message statuses. StatusDeleted is a view status only: the text was
// purged by retention or can no longer be opened (the author's key was
// shredded on account deletion).
const (
	StatusVisible = "visible"
	StatusHeld    = "held"
	StatusHidden  = "hidden"
	StatusDeleted = "deleted"
)

// Hold reasons. HoldCrisis is never published in chat.MessageHeld: a crisis
// message goes to a safety escalation, not to the moderation queue.
const (
	HoldCrisis   = "crisis"
	HoldNewcomer = "newcomer"
	HoldLink     = "link"
	HoldPhone    = "phone"
)

// Moderation actions (contract RoomModerationInput.action).
const (
	ActionHideMessage    = "hide_message"
	ActionRestoreMessage = "restore_message"
	ActionMute1h         = "mute_1h"
	ActionBan            = "ban"
	ActionUnban          = "unban"
	ActionSetSlowMode    = "set_slow_mode"
)

// Moderator kinds.
const (
	ModeratorStaff  = "staff"
	ModeratorMentor = "mentor"
	// ModeratorSystem is the audit actor role when applying moderation.Decided
	// (ActorID = the moderation item id).
	ModeratorSystem = "system"
)

// StaffModerationRoles are the staff roles with full chat moderation rights
// (staffauth role names).
var StaffModerationRoles = []string{"moderator", "admin"}

// MentorRole is the membership role a mentor must hold to moderate.
const MentorRole = "mentor"

// EscalationSource is the safety escalation source for crisis messages in
// chats (safety model.SourceChat on main since 2632013).
const EscalationSource = "chat"

// Limits fixed by the contract or the requirements.
const (
	// MaxTextRunes is the longest message, in characters (contract).
	MaxTextRunes = 2000
	// PageDefault and PageMax bound ListMessages (contract).
	PageDefault = 50
	PageMax     = 100
	// MaxSlowModeSeconds is the contract maximum for slow mode.
	MaxSlowModeSeconds = 3600
	// MuteDuration is the length of the mute_1h action.
	MuteDuration = time.Hour
	// MaxSubscribeRooms bounds one subscribe frame.
	MaxSubscribeRooms = 200
)

// Events published by chat, the notify event it asks for, and the events it
// consumes.
const (
	EventMessageHeld         = "chat.MessageHeld"
	EventMessageReported     = "chat.MessageReported"
	EventDeliveryRequested   = "notify.DeliveryRequested"
	EventMatchAccepted       = "circle.MatchAccepted"
	EventSosResponseAccepted = "emergency.SosResponseAccepted"
	EventContactAccepted     = "sharing.ContactAccepted"
	EventModerationDecided   = "moderation.Decided"
	EventVersion             = 1
)

// ConsumerDirectRooms is the platform.processed_events consumer name of the
// direct-room opener.
const ConsumerDirectRooms = "chat.DirectRoomOpener"

// ConsumerModerationDecided is the processed_events consumer name for
// moderation.Decided (kinds chat_message and report).
const ConsumerModerationDecided = "chat.moderation.Decided"

// Moderation.Decided kinds this context applies.
const (
	ModerationKindChatMessage = "chat_message"
	ModerationKindReport      = "report"
)

// Notify template and class of the content-free new-message push.
const (
	TemplateNewMessage = "chat.new_message"
	ClassNormal        = "normal"
)

// Timer kinds (ADR-0020).
const (
	// TimerCrisisEscalation opens the safety escalation of a crisis message
	// if the synchronous attempt after commit did not (subject = message id).
	TimerCrisisEscalation = "chat.CrisisEscalation"
	// TimerPush fans a visible message out to push recipients (subject = message id).
	TimerPush = "chat.PushFanout"
	// TimerRetention is the daily retention sweep (subject = RetentionSubject,
	// dedup key = retention date).
	TimerRetention = "chat.RetentionPurge"
)

// RetentionSubject is the fixed subject id of the retention timer.
var RetentionSubject = uuid.MustParse("019a2c00-0000-7000-8000-00000000c4a7")

// Audit codes written to platform.audit_log.
const (
	AuditObjectMessage = "chat.message"
	AuditObjectMember  = "chat.member"
	AuditObjectRoom    = "chat.room"
	AuditHide          = "chat.message.hide"
	AuditRestore       = "chat.message.restore"
	AuditMute          = "chat.member.mute"
	AuditBan           = "chat.member.ban"
	AuditUnban         = "chat.member.unban"
	AuditSlowMode      = "chat.room.slow_mode"
	AuditMentorAssign  = "chat.room.mentor_assign"
	AuditMentorRemove  = "chat.room.mentor_remove"
	AuditReportsClose  = "chat.message.reports_close"
	AuditLegalHoldOn   = "chat.message.legal_hold_on"
	AuditLegalHoldOff  = "chat.message.legal_hold_off"
)

// FieldText is the associated-data field of a message text
// (crypto.AssociatedData). Part of the schema: changing it makes stored
// ciphertexts unreadable.
const FieldText = "chat.messages.text"

// LockSend is the Tx.Lock scope that serialises one member's sends.
const LockSend = "chat.send"

// Errors returned by the service.
var (
	ErrUnauthorized = errors.New("chat: unauthorized")
	ErrForbidden    = errors.New("chat: forbidden")
	ErrNotFound     = errors.New("chat: not found")
	ErrInvalidInput = errors.New("chat: invalid input")
	ErrConflict     = errors.New("chat: conflict")
	ErrRateLimited  = errors.New("chat: rate limited")
	ErrSlowMode     = errors.New("chat: slow mode")
	ErrBanned       = errors.New("chat: banned or muted in this room")
	ErrNotMember    = errors.New("chat: not a member of the room")
)

// Room is one chat room.
type Room struct {
	ID              uuid.UUID
	Kind            string
	DistrictID      *uuid.UUID
	TopicCode       string
	TitleKey        string
	Origin          string
	OriginID        *uuid.UUID
	MemberLow       *uuid.UUID
	MemberHigh      *uuid.UUID
	SlowModeSeconds int
	LastSeq         int64
	CreatedAt       time.Time
}

// Group reports whether the room is a district or topic room.
func (r Room) Group() bool { return r.Kind == KindDistrict || r.Kind == KindTopic }

// DirectMember reports whether id is one of a direct room's two members.
func (r Room) DirectMember(id uuid.UUID) bool {
	return r.Kind == KindDirect && ((r.MemberLow != nil && *r.MemberLow == id) || (r.MemberHigh != nil && *r.MemberHigh == id))
}

// Other returns the other member of a direct room.
func (r Room) Other(id uuid.UUID) uuid.UUID {
	if r.MemberLow != nil && *r.MemberLow == id && r.MemberHigh != nil {
		return *r.MemberHigh
	}
	if r.MemberLow != nil {
		return *r.MemberLow
	}
	return uuid.Nil
}

// RoomMember is a member's membership of a room.
type RoomMember struct {
	RoomID     uuid.UUID
	MemberID   uuid.UUID
	JoinedAt   time.Time
	LeftAt     *time.Time
	Muted      bool
	LastPushAt *time.Time
}

// Active reports whether the member is (still) in the room.
func (m RoomMember) Active() bool { return m.LeftAt == nil }

// MemberRoom is a room the member is in, with her mute flag.
type MemberRoom struct {
	Room
	Muted bool
}

// Restriction is a member's moderation state in a room.
type Restriction struct {
	BannedAt   *time.Time
	MutedUntil *time.Time
}

// Blocks reports whether the restriction stops the member from posting at now.
func (r Restriction) Blocks(now time.Time) bool {
	return r.BannedAt != nil || (r.MutedUntil != nil && r.MutedUntil.After(now))
}

// Message is a stored message; Text is sealed (nil once purged).
type Message struct {
	ID           uuid.UUID
	RoomID       uuid.UUID
	Seq          int64
	AuthorID     uuid.UUID
	ClientID     uuid.UUID
	Text         []byte
	Status       string
	HoldReason   string
	EscalationID *uuid.UUID
	LegalHold    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	PurgedAt     *time.Time
}

// Opened is a message with its text opened (or not openable) and its
// author's display name, ready to be rendered for each reader.
type Opened struct {
	Message
	// PlainText is the opened text; empty with Deleted set when the text
	// was purged or the author's key is gone.
	PlainText  string
	Deleted    bool
	AuthorName string
}

// MessageView is a message as one reader sees it.
type MessageView struct {
	ID         uuid.UUID
	RoomID     uuid.UUID
	Seq        int64
	AuthorID   uuid.UUID
	AuthorName string
	Status     string
	Text       *string
	Mine       bool
	CreatedAt  time.Time
}

// View renders o for reader, or ok=false when the reader must not see it at
// all. The block filter is applied by the caller (it needs membership).
// Visibility:
//   - held: only the author (she sees her own message as held);
//   - hidden: everyone in the room sees a placeholder without text;
//   - purged or shredded: "deleted" without text;
//   - visible: everyone.
func View(o Opened, reader uuid.UUID) (MessageView, bool) {
	mine := o.AuthorID == reader
	v := MessageView{
		ID: o.ID, RoomID: o.RoomID, Seq: o.Seq, AuthorID: o.AuthorID, AuthorName: o.AuthorName,
		Status: o.Status, Mine: mine, CreatedAt: o.CreatedAt,
	}
	switch {
	case o.Status == StatusHeld && !mine:
		return MessageView{}, false
	case o.Deleted:
		v.Status = StatusDeleted
	case o.Status == StatusHidden:
	default:
		t := o.PlainText
		v.Text = &t
	}
	return v, true
}

// CrisisPhone is one crisis line from configuration (via safety).
type CrisisPhone struct {
	Number string
	Label  string
}

// CrisisNotice is the private crisis response to the author of a message
// that fired the crisis lexicon. EscalationID is nil while the escalation is
// still being opened by the retry timer.
type CrisisNotice struct {
	Phones       []CrisisPhone
	EscalationID *uuid.UUID
}

// SendResult is the outcome of a send.
type SendResult struct {
	Message MessageView
	// Crisis is set only for the author, when the crisis lexicon fired.
	Crisis *CrisisNotice
	// Replayed is true when the client id was already used (nothing new happened).
	Replayed bool
}

// RoomView is a room as one member sees it.
type RoomView struct {
	ID              uuid.UUID
	Kind            string
	Title           string
	Joined          bool
	Muted           bool
	LastSeq         int64
	SlowModeSeconds int
}

// StaffRoomMentor is an assigned mentor of a group room as shown to staff.
type StaffRoomMentor struct {
	MemberID    uuid.UUID
	DisplayName string
}

// StaffRoom is a group room with its member count and mentors for staff mentor assignment.
type StaffRoom struct {
	ID          uuid.UUID
	Kind        string
	Title       string
	MemberCount int
	Mentors     []StaffRoomMentor
}

// StaffRoomSummary is what the store returns for StaffRoom: mentor IDs only;
// the service adds their first names through the member-info port.
type StaffRoomSummary struct {
	ID          uuid.UUID
	Kind        string
	TitleKey    string
	MemberCount int
	MentorIDs   []uuid.UUID
}

// Profile is what chat reads about a member from membership. Never logged,
// stored or put into events.
type Profile struct {
	Verified   bool
	FirstName  string
	Lang       string
	DistrictID *uuid.UUID
}

// Moderation is one moderation request.
type Moderation struct {
	Action          string
	MessageID       *uuid.UUID
	MemberID        *uuid.UUID
	SlowModeSeconds *int
}

// ActionIsValid reports whether a is a known moderation action.
func ActionIsValid(a string) bool {
	switch a {
	case ActionHideMessage, ActionRestoreMessage, ActionMute1h, ActionBan, ActionUnban, ActionSetSlowMode:
		return true
	}
	return false
}

// Allowed reports whether a moderator kind may take action. Staff moderators
// may take every action; a room's mentor only hides, restores and mutes for
// an hour (ADR-0016: limited rights, no personal data).
func Allowed(moderator, action string) bool {
	switch moderator {
	case ModeratorStaff:
		return ActionIsValid(action)
	case ModeratorMentor:
		return action == ActionHideMessage || action == ActionRestoreMessage || action == ActionMute1h
	}
	return false
}

// IsStaffModerator reports whether roles grant full chat moderation.
func IsStaffModerator(roles []string) bool {
	for _, r := range roles {
		if slices.Contains(StaffModerationRoles, r) {
			return true
		}
	}
	return false
}

// ReportReasons are the contract's report reasons.
var ReportReasons = []string{"harassment", "scam_or_fundraising", "miracle_cure", "unsafe_content", "child_safety", "spam", "other"}

// ValidateText checks a message text: valid UTF-8, not blank, at most
// MaxTextRunes characters.
func ValidateText(text string) error {
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > MaxTextRunes {
		return ErrInvalidInput
	}
	return nil
}

// ValidateClientID checks a client message id: a UUIDv7 (contract).
func ValidateClientID(id uuid.UUID) error {
	if id == uuid.Nil || id.Version() != 7 {
		return ErrInvalidInput
	}
	return nil
}

// OrderPair returns a and b ordered (low, high) for direct-room keys.
func OrderPair(a, b uuid.UUID) (low, high uuid.UUID) {
	if strings.Compare(a.String(), b.String()) < 0 {
		return a, b
	}
	return b, a
}

// Notice kinds sent through the fan-out (NATS core).
const (
	NoticeNew     = "new"
	NoticeChanged = "changed"
	NoticeLeft    = "left"
)

// Notice is a fan-out signal for one room. It carries IDs only: every
// gateway reads the message from PostgreSQL (the source of truth), so no
// text crosses the broker.
type Notice struct {
	Kind      string     `json:"kind"`
	RoomID    uuid.UUID  `json:"room_id"`
	MessageID *uuid.UUID `json:"message_id,omitempty"`
	Seq       int64      `json:"seq,omitempty"`
	// MemberID is the member who left or was banned (kind left).
	MemberID *uuid.UUID `json:"member_id,omitempty"`
}

// RoomSubject is the NATS core subject of a room's fan-out.
func RoomSubject(roomID uuid.UUID) string { return "birge.chat.room." + roomID.String() }

// AuditEntry is one platform.audit_log row. Reason carries fixed codes only.
type AuditEntry struct {
	ActorID    uuid.UUID
	ActorRole  string
	Action     string
	ObjectType string
	ObjectID   uuid.UUID
	Reason     string
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

// MatchAcceptedData is the payload of circle.MatchAccepted v1.
type MatchAcceptedData struct {
	MatchID uuid.UUID `json:"match_id"`
	MemberA uuid.UUID `json:"member_a"`
	MemberB uuid.UUID `json:"member_b"`
}

// SosResponseAcceptedData is the payload of emergency.SosResponseAccepted v1.
type SosResponseAcceptedData struct {
	SosID       uuid.UUID `json:"sos_id"`
	AuthorID    uuid.UUID `json:"author_id"`
	ResponderID uuid.UUID `json:"responder_id"`
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

// DeliveryRequestedData is the payload of notify.DeliveryRequested v1. It
// carries the template key and the room id only, never text or names.
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
var deliveryNamespace = uuid.MustParse("6f1d7c2a-5b8e-4e93-9a41-0c2d3e4f5a6b")

// NewMessagePush is the content-free push to memberID about a message in
// roomID. The request id is deterministic in the message and the
// recipient, so a retried timer cannot push twice.
func NewMessagePush(messageID, roomID, memberID uuid.UUID) Event {
	id := memberID
	return Event{
		Type:    EventDeliveryRequested,
		Version: EventVersion,
		Data: DeliveryRequestedData{
			RequestID: uuid.NewSHA1(deliveryNamespace, []byte(messageID.String()+"|"+TemplateNewMessage+"|member:"+memberID.String())),
			Recipient: DeliveryRecipient{MemberID: &id},
			Class:     ClassNormal,
			Template:  TemplateNewMessage,
			Params:    map[string]string{"room_id": roomID.String()},
		},
	}
}

// MessageHeld builds chat.MessageHeld for a held message (never for crisis).
func MessageHeld(m Message) Event {
	return Event{Type: EventMessageHeld, Version: EventVersion, Data: MessageHeldData{MessageID: m.ID, RoomID: m.RoomID, Reason: m.HoldReason}}
}

// MessageReported builds chat.MessageReported.
func MessageReported(messageID, roomID uuid.UUID, reason string) Event {
	return Event{Type: EventMessageReported, Version: EventVersion, Data: MessageReportedData{MessageID: messageID, RoomID: roomID, Reason: reason}}
}

// Retention returns how long messages of a room kind are kept.
func Retention(kind string, group, direct time.Duration) time.Duration {
	if kind == KindDirect {
		return direct
	}
	return group
}

// NextRetentionRun returns the first daily run time at hour:minute UTC
// strictly after now.
func NextRetentionRun(now time.Time, at time.Duration) time.Time {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	next := day.Add(at)
	if !next.After(now) {
		next = day.AddDate(0, 0, 1).Add(at)
	}
	return next
}
