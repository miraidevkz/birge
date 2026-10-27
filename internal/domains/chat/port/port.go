// Package port defines the interfaces the chat service and gateway need: the
// store (rooms, members, messages, reports, outbox, timers, audit,
// processed events), field sealing, the room fan-out, member authentication
// for the WebSocket, and what chat reads from other contexts (member
// profiles, blocks and the mentor role from membership; crisis detection,
// crisis phones and escalations from safety). Adapters onto other contexts
// are wired later; tests use the fakes in chattest.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/platform/auth"
)

// Sealer encrypts and decrypts one member's fields with that member's data
// key (platform crypto, ADR-0009). A message text belongs to its author, so
// deleting her account makes her messages unreadable ("message deleted").
type Sealer interface {
	Seal(ctx context.Context, memberID uuid.UUID, plaintext, associatedData []byte) ([]byte, error)
	Open(ctx context.Context, memberID uuid.UUID, ciphertext, associatedData []byte) ([]byte, error)
}

// MemberInfo reads member profiles owned by membership. Results are never
// logged, stored or put into events.
type MemberInfo interface {
	// Profiles returns the profiles of the given members; unknown or deleted
	// members are absent from the map.
	Profiles(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]model.Profile, error)
}

// Blocks reads member blocks owned by membership (ADR-0019).
type Blocks interface {
	// IsBlocked reports whether a blocked b or b blocked a.
	IsBlocked(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// Mentors reads the membership role "mentor".
type Mentors interface {
	// IsMentor reports whether the member currently holds the mentor role.
	IsMentor(ctx context.Context, memberID uuid.UUID) (bool, error)
}

// Crisis is the safety context's crisis path (ADR-0023). Detect and
// CrisisPhones are local and never fail; OpenEscalation is idempotent per
// member while an escalation is open.
type Crisis interface {
	// Detect runs the level-1 lexicon; lang only orders the languages.
	Detect(text, lang string) bool
	// CrisisPhones returns the configured crisis lines.
	CrisisPhones() []model.CrisisPhone
	// OpenEscalation opens (or returns the open) escalation of the member.
	OpenEscalation(ctx context.Context, memberID uuid.UUID, source string) (uuid.UUID, error)
}

// Fanout publishes room notices to every gateway replica (NATS core,
// birge.chat.room.<id>). Delivery is best-effort: clients recover missed
// notices by sequence from PostgreSQL.
type Fanout interface {
	Publish(ctx context.Context, n model.Notice) error
}

// Bus is the receiving side of the fan-out used by the gateway.
type Bus interface {
	// Subscribe calls fn for every notice on the room's subject until the
	// returned function is called.
	Subscribe(roomID uuid.UUID, fn func(model.Notice)) (unsubscribe func(), err error)
}

// Authenticator validates a member session token sent in the WebSocket's
// first frame (wired to the session validator). It returns a principal with
// MemberID set, or an error.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (auth.Principal, error)
}

// Store is the persistence port for chat tables and platform hooks.
type Store interface {
	// InTx runs fn in one database transaction: committed when fn returns
	// nil, rolled back otherwise.
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction. Every write a use case makes
// (rows, outbox events, timers, audit, processed events) goes through the
// same Tx so they commit or roll back together.
type Tx interface {
	// Lock takes a transaction-scoped lock on (scope, id). Sends take the
	// author's lock before any room row lock, never after.
	Lock(ctx context.Context, scope string, id uuid.UUID) error

	// InsertGroupRoom inserts a district or topic room; inserted is false
	// when that district or topic already has one.
	InsertGroupRoom(ctx context.Context, r model.Room) (created model.Room, inserted bool, err error)
	// GetDistrictRoom and GetTopicRoom return the group room, or model.ErrNotFound.
	GetDistrictRoom(ctx context.Context, districtID uuid.UUID) (model.Room, error)
	GetTopicRoom(ctx context.Context, code string) (model.Room, error)
	// InsertDirectRoom inserts a direct room; inserted is false when the
	// origin already opened one for the pair.
	InsertDirectRoom(ctx context.Context, r model.Room) (created model.Room, inserted bool, err error)
	// GetDirectRoom returns the room of origin/originID for the ordered pair, or model.ErrNotFound.
	GetDirectRoom(ctx context.Context, origin string, originID, low, high uuid.UUID) (model.Room, error)
	// GetRoom returns the room, or model.ErrNotFound; forUpdate keeps the row locked.
	GetRoom(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Room, error)
	// NextSeq increments and returns the room's sequence, locking the room
	// row until the transaction ends.
	NextSeq(ctx context.Context, roomID uuid.UUID) (int64, error)
	// SetSlowMode sets the room's slow mode.
	SetSlowMode(ctx context.Context, roomID uuid.UUID, seconds int) error
	// ListJoinableGroupRooms returns the district room of districtID (when
	// set) and every topic room.
	ListJoinableGroupRooms(ctx context.Context, districtID *uuid.UUID) ([]model.Room, error)
	// ListMemberRooms returns the rooms the member is in.
	ListMemberRooms(ctx context.Context, memberID uuid.UUID) ([]model.MemberRoom, error)

	// GetRoomMember returns the membership row (also a left one), or model.ErrNotFound.
	GetRoomMember(ctx context.Context, roomID, memberID uuid.UUID) (model.RoomMember, error)
	// JoinRoom adds or re-adds the member.
	JoinRoom(ctx context.Context, roomID, memberID uuid.UUID, at time.Time) error
	// AddRoomMemberIfAbsent adds a member once and never re-adds one who left.
	AddRoomMemberIfAbsent(ctx context.Context, roomID, memberID uuid.UUID, at time.Time) error
	// LeaveRoom marks the member left; left is false when she was not in the room.
	LeaveRoom(ctx context.Context, roomID, memberID uuid.UUID, at time.Time) (left bool, err error)
	// SetMute sets the member's push mute; updated is false when she is not in the room.
	SetMute(ctx context.Context, roomID, memberID uuid.UUID, muted bool) (updated bool, err error)
	// TouchPresence records that the member is connected to the room until until.
	TouchPresence(ctx context.Context, roomID, memberID uuid.UUID, until time.Time) error
	// ListPushCandidates returns members to push for a message by authorID:
	// in the room, not muted, not connected at now, not pushed since pushedBefore.
	ListPushCandidates(ctx context.Context, roomID, authorID uuid.UUID, now, pushedBefore time.Time) ([]uuid.UUID, error)
	// MarkPushed records a push to the member for the room.
	MarkPushed(ctx context.Context, roomID, memberID uuid.UUID, at time.Time) error

	// GetRestriction returns the member's restriction in the room (zero when none).
	GetRestriction(ctx context.Context, roomID, memberID uuid.UUID) (model.Restriction, error)
	// SetBan bans (at set) or unbans (at nil) the member in the room.
	SetBan(ctx context.Context, roomID, memberID uuid.UUID, at *time.Time, now time.Time) error
	// SetMutedUntil mutes the member in the room until until.
	SetMutedUntil(ctx context.Context, roomID, memberID uuid.UUID, until, now time.Time) error
	// AssignMentor and RemoveMentor change the room's mentors.
	AssignMentor(ctx context.Context, roomID, memberID, by uuid.UUID, at time.Time) error
	RemoveMentor(ctx context.Context, roomID, memberID uuid.UUID, at time.Time) (bool, error)
	// IsRoomMentor reports whether the member is an assigned mentor of the room.
	IsRoomMentor(ctx context.Context, roomID, memberID uuid.UUID) (bool, error)

	// InsertMessage inserts a message whose text is already sealed;
	// inserted is false when the author already used m.ClientID.
	InsertMessage(ctx context.Context, m model.Message) (created model.Message, inserted bool, err error)
	// GetMessageByClientID returns the author's message with clientID, or model.ErrNotFound.
	GetMessageByClientID(ctx context.Context, authorID, clientID uuid.UUID) (model.Message, error)
	// GetMessage returns the message, or model.ErrNotFound; forUpdate keeps the row locked.
	GetMessage(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Message, error)
	// ListMessagesAfter returns messages with seq > after, ascending.
	ListMessagesAfter(ctx context.Context, roomID uuid.UUID, after int64, limit int32) ([]model.Message, error)
	// ListMessagesBefore returns messages with seq < before, descending.
	ListMessagesBefore(ctx context.Context, roomID uuid.UUID, before int64, limit int32) ([]model.Message, error)
	// CountAuthorMessagesSince counts the author's messages after since (all rooms).
	CountAuthorMessagesSince(ctx context.Context, authorID uuid.UUID, since time.Time) (int, error)
	// CountAuthorGroupMessages counts the author's group-room messages, at most upTo.
	CountAuthorGroupMessages(ctx context.Context, authorID uuid.UUID, upTo int) (int, error)
	// LastAuthorMessageAt returns when the author last posted in the room, or nil.
	LastAuthorMessageAt(ctx context.Context, roomID, authorID uuid.UUID) (*time.Time, error)
	// SetMessageStatus changes the message status.
	SetMessageStatus(ctx context.Context, id uuid.UUID, status string, at time.Time) (model.Message, error)
	// SetMessageEscalation records the escalation of a crisis message once.
	SetMessageEscalation(ctx context.Context, id, escalationID uuid.UUID) error
	// SetLegalHold sets or clears the message's legal hold.
	SetLegalHold(ctx context.Context, id uuid.UUID, on bool) (bool, error)
	// PurgeExpired clears the text of up to limit expired messages of the
	// kinds, skipping legal holds and open reports; returns how many.
	PurgeExpired(ctx context.Context, kinds []string, createdBefore, now time.Time, limit int32) (int64, error)

	// InsertReport records a report; inserted is false when the member
	// already reported the message.
	InsertReport(ctx context.Context, id, messageID, roomID, reporterID uuid.UUID, reason string, at time.Time) (inserted bool, err error)
	// CloseReports closes the open reports of the message.
	CloseReports(ctx context.Context, messageID uuid.UUID, at time.Time) (int64, error)

	// MarkProcessed records that consumer handled eventID; first is false
	// for a duplicate.
	MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (first bool, err error)
	// Publish enqueues e in this transaction's outbox (ADR-0007).
	Publish(ctx context.Context, now time.Time, e model.Event) error
	// Schedule creates a platform timer; scheduled is false on a dedup hit.
	Schedule(ctx context.Context, kind string, subjectID uuid.UUID, dedupKey string, dueAt time.Time) (scheduled bool, err error)
	// CancelTimers cancels the active timers of kind for the subject.
	CancelTimers(ctx context.Context, kind string, subjectID uuid.UUID, at time.Time) error
	// Audit appends to platform.audit_log in this transaction.
	Audit(ctx context.Context, e model.AuditEntry) error
}
