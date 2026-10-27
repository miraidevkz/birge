// Package adapter implements the chat ports on PostgreSQL (schema chat, sqlc
// in ../store) with the platform hooks (outbox, timers, audit,
// processed_events), the room fan-out on NATS core, the JetStream consumer
// that opens direct rooms, the timer handlers and the DEMO room seed. Ports
// onto other contexts (membership, safety) are wired later.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	chatstore "birge/internal/domains/chat/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// PostgresStore implements port.Store on PostgreSQL.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

var _ port.Store = (*PostgresStore)(nil)

// InTx runs fn in a transaction (READ COMMITTED, the PostgreSQL default;
// sends serialise through Tx.Lock and the room row lock of Tx.NextSeq).
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(Bind(pgxTx))
	})
}

// Bind returns a port.Tx working inside an existing transaction, such as the
// one the timer worker passes to a handler.
func Bind(tx pgx.Tx) port.Tx {
	return &postgresTx{q: chatstore.New(tx), platform: platformstore.New(tx)}
}

type postgresTx struct {
	q        *chatstore.Queries
	platform *platformstore.Queries
}

func notFound(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Lock implements port.Tx.
func (t *postgresTx) Lock(ctx context.Context, scope string, id uuid.UUID) error {
	if err := t.q.ChatAdvisoryXactLock(ctx, scope+"|"+id.String()); err != nil {
		return fmt.Errorf("lock %s: %w", scope, err)
	}
	return nil
}

// InsertGroupRoom implements port.Tx.
func (t *postgresTx) InsertGroupRoom(ctx context.Context, r model.Room) (model.Room, bool, error) {
	row, err := t.q.InsertGroupRoom(ctx, chatstore.InsertGroupRoomParams{
		ID: r.ID, Kind: r.Kind, DistrictID: r.DistrictID, TopicCode: strPtr(r.TopicCode), TitleKey: strPtr(r.TitleKey), CreatedAt: r.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Room{}, false, nil
	}
	if err != nil {
		return model.Room{}, false, fmt.Errorf("insert group room: %w", err)
	}
	return mapRoom(row), true, nil
}

// GetDistrictRoom implements port.Tx.
func (t *postgresTx) GetDistrictRoom(ctx context.Context, districtID uuid.UUID) (model.Room, error) {
	row, err := t.q.GetDistrictRoom(ctx, &districtID)
	if err != nil {
		return model.Room{}, notFound(err, "get district room")
	}
	return mapRoom(row), nil
}

// GetTopicRoom implements port.Tx.
func (t *postgresTx) GetTopicRoom(ctx context.Context, code string) (model.Room, error) {
	row, err := t.q.GetTopicRoom(ctx, &code)
	if err != nil {
		return model.Room{}, notFound(err, "get topic room")
	}
	return mapRoom(row), nil
}

// InsertDirectRoom implements port.Tx.
func (t *postgresTx) InsertDirectRoom(ctx context.Context, r model.Room) (model.Room, bool, error) {
	row, err := t.q.InsertDirectRoom(ctx, chatstore.InsertDirectRoomParams{
		ID: r.ID, Origin: strPtr(r.Origin), OriginID: r.OriginID, MemberLow: r.MemberLow, MemberHigh: r.MemberHigh, CreatedAt: r.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Room{}, false, nil
	}
	if err != nil {
		return model.Room{}, false, fmt.Errorf("insert direct room: %w", err)
	}
	return mapRoom(row), true, nil
}

// GetDirectRoom implements port.Tx.
func (t *postgresTx) GetDirectRoom(ctx context.Context, origin string, originID, low, high uuid.UUID) (model.Room, error) {
	row, err := t.q.GetDirectRoom(ctx, chatstore.GetDirectRoomParams{Origin: &origin, OriginID: &originID, MemberLow: &low, MemberHigh: &high})
	if err != nil {
		return model.Room{}, notFound(err, "get direct room")
	}
	return mapRoom(row), nil
}

// GetRoom implements port.Tx.
func (t *postgresTx) GetRoom(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Room, error) {
	var row chatstore.ChatRoom
	var err error
	if forUpdate {
		row, err = t.q.GetRoomForUpdate(ctx, id)
	} else {
		row, err = t.q.GetRoom(ctx, id)
	}
	if err != nil {
		return model.Room{}, notFound(err, "get room")
	}
	return mapRoom(row), nil
}

// NextSeq implements port.Tx.
func (t *postgresTx) NextSeq(ctx context.Context, roomID uuid.UUID) (int64, error) {
	seq, err := t.q.NextRoomSeq(ctx, roomID)
	if err != nil {
		return 0, notFound(err, "next room seq")
	}
	return seq, nil
}

// SetSlowMode implements port.Tx.
func (t *postgresTx) SetSlowMode(ctx context.Context, roomID uuid.UUID, seconds int) error {
	n, err := t.q.SetRoomSlowMode(ctx, chatstore.SetRoomSlowModeParams{
		SlowModeSeconds: int32(seconds), //nolint:gosec // validated 0..3600
		ID:              roomID,
	})
	if err != nil {
		return fmt.Errorf("set slow mode: %w", err)
	}
	if n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// ListJoinableGroupRooms implements port.Tx.
func (t *postgresTx) ListJoinableGroupRooms(ctx context.Context, districtID *uuid.UUID) ([]model.Room, error) {
	rows, err := t.q.ListJoinableGroupRooms(ctx, districtID)
	if err != nil {
		return nil, fmt.Errorf("list joinable rooms: %w", err)
	}
	out := make([]model.Room, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapRoom(r))
	}
	return out, nil
}

// ListMemberRooms implements port.Tx.
func (t *postgresTx) ListMemberRooms(ctx context.Context, memberID uuid.UUID) ([]model.MemberRoom, error) {
	rows, err := t.q.ListMemberRooms(ctx, memberID)
	if err != nil {
		return nil, fmt.Errorf("list member rooms: %w", err)
	}
	out := make([]model.MemberRoom, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.MemberRoom{Room: mapRoom(chatstore.ChatRoom{
			ID: r.ID, Kind: r.Kind, DistrictID: r.DistrictID, TopicCode: r.TopicCode, TitleKey: r.TitleKey,
			Origin: r.Origin, OriginID: r.OriginID, MemberLow: r.MemberLow, MemberHigh: r.MemberHigh,
			SlowModeSeconds: r.SlowModeSeconds, LastSeq: r.LastSeq, CreatedAt: r.CreatedAt,
		}), Muted: r.Muted})
	}
	return out, nil
}

// GetRoomMember implements port.Tx.
func (t *postgresTx) GetRoomMember(ctx context.Context, roomID, memberID uuid.UUID) (model.RoomMember, error) {
	row, err := t.q.GetRoomMember(ctx, chatstore.GetRoomMemberParams{RoomID: roomID, MemberID: memberID})
	if err != nil {
		return model.RoomMember{}, notFound(err, "get room member")
	}
	return model.RoomMember{RoomID: row.RoomID, MemberID: row.MemberID, JoinedAt: row.JoinedAt, LeftAt: row.LeftAt, Muted: row.Muted, LastPushAt: row.LastPushAt}, nil
}

// JoinRoom implements port.Tx.
func (t *postgresTx) JoinRoom(ctx context.Context, roomID, memberID uuid.UUID, at time.Time) error {
	if err := t.q.JoinRoom(ctx, chatstore.JoinRoomParams{RoomID: roomID, MemberID: memberID, JoinedAt: at}); err != nil {
		return fmt.Errorf("join room: %w", err)
	}
	return nil
}

// AddRoomMemberIfAbsent implements port.Tx.
func (t *postgresTx) AddRoomMemberIfAbsent(ctx context.Context, roomID, memberID uuid.UUID, at time.Time) error {
	if err := t.q.AddRoomMemberIfAbsent(ctx, chatstore.AddRoomMemberIfAbsentParams{RoomID: roomID, MemberID: memberID, JoinedAt: at}); err != nil {
		return fmt.Errorf("add room member: %w", err)
	}
	return nil
}

// LeaveRoom implements port.Tx.
func (t *postgresTx) LeaveRoom(ctx context.Context, roomID, memberID uuid.UUID, at time.Time) (bool, error) {
	n, err := t.q.LeaveRoom(ctx, chatstore.LeaveRoomParams{LeftAt: &at, RoomID: roomID, MemberID: memberID})
	if err != nil {
		return false, fmt.Errorf("leave room: %w", err)
	}
	return n == 1, nil
}

// SetMute implements port.Tx.
func (t *postgresTx) SetMute(ctx context.Context, roomID, memberID uuid.UUID, muted bool) (bool, error) {
	n, err := t.q.SetRoomMute(ctx, chatstore.SetRoomMuteParams{Muted: muted, RoomID: roomID, MemberID: memberID})
	if err != nil {
		return false, fmt.Errorf("set room mute: %w", err)
	}
	return n == 1, nil
}

// TouchPresence implements port.Tx.
func (t *postgresTx) TouchPresence(ctx context.Context, roomID, memberID uuid.UUID, until time.Time) error {
	if _, err := t.q.TouchPresence(ctx, chatstore.TouchPresenceParams{ConnectedUntil: &until, RoomID: roomID, MemberID: memberID}); err != nil {
		return fmt.Errorf("touch presence: %w", err)
	}
	return nil
}

// ListPushCandidates implements port.Tx.
func (t *postgresTx) ListPushCandidates(ctx context.Context, roomID, authorID uuid.UUID, now, pushedBefore time.Time) ([]uuid.UUID, error) {
	ids, err := t.q.ListPushCandidates(ctx, chatstore.ListPushCandidatesParams{RoomID: roomID, AuthorID: authorID, Now: &now, PushedBefore: &pushedBefore})
	if err != nil {
		return nil, fmt.Errorf("list push candidates: %w", err)
	}
	return ids, nil
}

// MarkPushed implements port.Tx.
func (t *postgresTx) MarkPushed(ctx context.Context, roomID, memberID uuid.UUID, at time.Time) error {
	if err := t.q.MarkPushed(ctx, chatstore.MarkPushedParams{PushedAt: &at, RoomID: roomID, MemberID: memberID}); err != nil {
		return fmt.Errorf("mark pushed: %w", err)
	}
	return nil
}

// GetRestriction implements port.Tx.
func (t *postgresTx) GetRestriction(ctx context.Context, roomID, memberID uuid.UUID) (model.Restriction, error) {
	row, err := t.q.GetRestriction(ctx, chatstore.GetRestrictionParams{RoomID: roomID, MemberID: memberID})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Restriction{}, nil
	}
	if err != nil {
		return model.Restriction{}, fmt.Errorf("get restriction: %w", err)
	}
	return model.Restriction{BannedAt: row.BannedAt, MutedUntil: row.MutedUntil}, nil
}

// SetBan implements port.Tx.
func (t *postgresTx) SetBan(ctx context.Context, roomID, memberID uuid.UUID, at *time.Time, now time.Time) error {
	if err := t.q.SetBan(ctx, chatstore.SetBanParams{RoomID: roomID, MemberID: memberID, BannedAt: at, UpdatedAt: now}); err != nil {
		return fmt.Errorf("set ban: %w", err)
	}
	return nil
}

// SetMutedUntil implements port.Tx.
func (t *postgresTx) SetMutedUntil(ctx context.Context, roomID, memberID uuid.UUID, until, now time.Time) error {
	if err := t.q.SetMutedUntil(ctx, chatstore.SetMutedUntilParams{RoomID: roomID, MemberID: memberID, MutedUntil: &until, UpdatedAt: now}); err != nil {
		return fmt.Errorf("set muted until: %w", err)
	}
	return nil
}

// AssignMentor implements port.Tx.
func (t *postgresTx) AssignMentor(ctx context.Context, roomID, memberID, by uuid.UUID, at time.Time) error {
	if err := t.q.UpsertRoomMentor(ctx, chatstore.UpsertRoomMentorParams{RoomID: roomID, MemberID: memberID, AssignedBy: by, AssignedAt: at}); err != nil {
		return fmt.Errorf("assign mentor: %w", err)
	}
	return nil
}

// RemoveMentor implements port.Tx.
func (t *postgresTx) RemoveMentor(ctx context.Context, roomID, memberID uuid.UUID, at time.Time) (bool, error) {
	n, err := t.q.RemoveRoomMentor(ctx, chatstore.RemoveRoomMentorParams{RemovedAt: &at, RoomID: roomID, MemberID: memberID})
	if err != nil {
		return false, fmt.Errorf("remove mentor: %w", err)
	}
	return n == 1, nil
}

// IsRoomMentor implements port.Tx.
func (t *postgresTx) IsRoomMentor(ctx context.Context, roomID, memberID uuid.UUID) (bool, error) {
	ok, err := t.q.IsRoomMentor(ctx, chatstore.IsRoomMentorParams{RoomID: roomID, MemberID: memberID})
	if err != nil {
		return false, fmt.Errorf("is room mentor: %w", err)
	}
	return ok, nil
}

// InsertMessage implements port.Tx.
func (t *postgresTx) InsertMessage(ctx context.Context, m model.Message) (model.Message, bool, error) {
	row, err := t.q.InsertMessage(ctx, chatstore.InsertMessageParams{
		ID: m.ID, RoomID: m.RoomID, Seq: m.Seq, AuthorID: m.AuthorID, ClientID: m.ClientID, Body: m.Text,
		Status: m.Status, HoldReason: strPtr(m.HoldReason), CreatedAt: m.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Message{}, false, nil
	}
	if err != nil {
		return model.Message{}, false, fmt.Errorf("insert message: %w", err)
	}
	return mapMessage(row), true, nil
}

// GetMessageByClientID implements port.Tx.
func (t *postgresTx) GetMessageByClientID(ctx context.Context, authorID, clientID uuid.UUID) (model.Message, error) {
	row, err := t.q.GetMessageByClientID(ctx, chatstore.GetMessageByClientIDParams{AuthorID: authorID, ClientID: clientID})
	if err != nil {
		return model.Message{}, notFound(err, "get message by client id")
	}
	return mapMessage(row), nil
}

// GetMessage implements port.Tx.
func (t *postgresTx) GetMessage(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Message, error) {
	var row chatstore.ChatMessage
	var err error
	if forUpdate {
		row, err = t.q.GetMessageForUpdate(ctx, id)
	} else {
		row, err = t.q.GetMessage(ctx, id)
	}
	if err != nil {
		return model.Message{}, notFound(err, "get message")
	}
	return mapMessage(row), nil
}

// ListMessagesAfter implements port.Tx.
func (t *postgresTx) ListMessagesAfter(ctx context.Context, roomID uuid.UUID, after int64, limit int32) ([]model.Message, error) {
	rows, err := t.q.ListMessagesAfter(ctx, chatstore.ListMessagesAfterParams{RoomID: roomID, AfterSeq: after, PageLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list messages after: %w", err)
	}
	return mapMessages(rows), nil
}

// ListMessagesBefore implements port.Tx.
func (t *postgresTx) ListMessagesBefore(ctx context.Context, roomID uuid.UUID, before int64, limit int32) ([]model.Message, error) {
	rows, err := t.q.ListMessagesBefore(ctx, chatstore.ListMessagesBeforeParams{RoomID: roomID, BeforeSeq: before, PageLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list messages before: %w", err)
	}
	return mapMessages(rows), nil
}

// CountAuthorMessagesSince implements port.Tx.
func (t *postgresTx) CountAuthorMessagesSince(ctx context.Context, authorID uuid.UUID, since time.Time) (int, error) {
	n, err := t.q.CountAuthorMessagesSince(ctx, chatstore.CountAuthorMessagesSinceParams{AuthorID: authorID, Since: since})
	if err != nil {
		return 0, fmt.Errorf("count author messages: %w", err)
	}
	return int(n), nil
}

// CountAuthorGroupMessages implements port.Tx.
func (t *postgresTx) CountAuthorGroupMessages(ctx context.Context, authorID uuid.UUID, upTo int) (int, error) {
	n, err := t.q.CountAuthorGroupMessages(ctx, chatstore.CountAuthorGroupMessagesParams{
		AuthorID: authorID,
		UpTo:     int32(upTo), //nolint:gosec // a small configured limit
	})
	if err != nil {
		return 0, fmt.Errorf("count author group messages: %w", err)
	}
	return int(n), nil
}

// LastAuthorMessageAt implements port.Tx.
func (t *postgresTx) LastAuthorMessageAt(ctx context.Context, roomID, authorID uuid.UUID) (*time.Time, error) {
	at, err := t.q.LastAuthorMessageAt(ctx, chatstore.LastAuthorMessageAtParams{RoomID: roomID, AuthorID: authorID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last author message: %w", err)
	}
	return &at, nil
}

// SetMessageStatus implements port.Tx.
func (t *postgresTx) SetMessageStatus(ctx context.Context, id uuid.UUID, status string, at time.Time) (model.Message, error) {
	row, err := t.q.SetMessageStatus(ctx, chatstore.SetMessageStatusParams{Status: status, UpdatedAt: at, ID: id})
	if err != nil {
		return model.Message{}, notFound(err, "set message status")
	}
	return mapMessage(row), nil
}

// SetMessageEscalation implements port.Tx.
func (t *postgresTx) SetMessageEscalation(ctx context.Context, id, escalationID uuid.UUID) error {
	if _, err := t.q.SetMessageEscalation(ctx, chatstore.SetMessageEscalationParams{EscalationID: &escalationID, ID: id}); err != nil {
		return fmt.Errorf("set message escalation: %w", err)
	}
	return nil
}

// SetLegalHold implements port.Tx.
func (t *postgresTx) SetLegalHold(ctx context.Context, id uuid.UUID, on bool) (bool, error) {
	n, err := t.q.SetMessageLegalHold(ctx, chatstore.SetMessageLegalHoldParams{LegalHold: on, ID: id})
	if err != nil {
		return false, fmt.Errorf("set legal hold: %w", err)
	}
	return n == 1, nil
}

// PurgeExpired implements port.Tx.
func (t *postgresTx) PurgeExpired(ctx context.Context, kinds []string, createdBefore, now time.Time, limit int32) (int64, error) {
	n, err := t.q.PurgeExpiredMessages(ctx, chatstore.PurgeExpiredMessagesParams{PurgedAt: &now, CreatedBefore: createdBefore, Kinds: kinds, PageLimit: limit})
	if err != nil {
		return 0, fmt.Errorf("purge expired messages: %w", err)
	}
	return n, nil
}

// InsertReport implements port.Tx.
func (t *postgresTx) InsertReport(ctx context.Context, id, messageID, roomID, reporterID uuid.UUID, reason string, at time.Time) (bool, error) {
	_, err := t.q.InsertReport(ctx, chatstore.InsertReportParams{ID: id, MessageID: messageID, RoomID: roomID, ReporterID: reporterID, Reason: reason, CreatedAt: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert report: %w", err)
	}
	return true, nil
}

// CloseReports implements port.Tx.
func (t *postgresTx) CloseReports(ctx context.Context, messageID uuid.UUID, at time.Time) (int64, error) {
	n, err := t.q.CloseReports(ctx, chatstore.CloseReportsParams{ClosedAt: &at, MessageID: messageID})
	if err != nil {
		return 0, fmt.Errorf("close reports: %w", err)
	}
	return n, nil
}

// MarkProcessed implements port.Tx.
func (t *postgresTx) MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (bool, error) {
	n, err := t.platform.MarkEventProcessed(ctx, platformstore.MarkEventProcessedParams{Consumer: consumer, EventID: eventID})
	if err != nil {
		return false, fmt.Errorf("mark event processed: %w", err)
	}
	return n == 1, nil
}

// Publish implements port.Tx.
func (t *postgresTx) Publish(ctx context.Context, now time.Time, e model.Event) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{Type: e.Type, Version: e.Version, Data: e.Data})
	return err
}

// Schedule implements port.Tx.
func (t *postgresTx) Schedule(ctx context.Context, kind string, subjectID uuid.UUID, dedupKey string, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{Kind: kind, SubjectID: subjectID, DedupKey: dedupKey, DueAt: dueAt, Payload: map[string]string{}})
}

// CancelTimers implements port.Tx.
func (t *postgresTx) CancelTimers(ctx context.Context, kind string, subjectID uuid.UUID, at time.Time) error {
	if _, err := timers.Cancel(ctx, t.platform, at, kind, subjectID); err != nil {
		return fmt.Errorf("cancel timers: %w", err)
	}
	return nil
}

// Audit implements port.Tx.Audit in the current transaction, so the entry
// commits or rolls back with the action it records.
func (t *postgresTx) Audit(ctx context.Context, e model.AuditEntry) error {
	return audit.Record(ctx, t.platform, audit.Entry{
		ActorID: e.ActorID, ActorRole: e.ActorRole, Action: e.Action, ObjectType: e.ObjectType, ObjectID: e.ObjectID, Reason: e.Reason,
	})
}

func mapRoom(r chatstore.ChatRoom) model.Room {
	return model.Room{
		ID: r.ID, Kind: r.Kind, DistrictID: r.DistrictID, TopicCode: deref(r.TopicCode), TitleKey: deref(r.TitleKey),
		Origin: deref(r.Origin), OriginID: r.OriginID, MemberLow: r.MemberLow, MemberHigh: r.MemberHigh,
		SlowModeSeconds: int(r.SlowModeSeconds), LastSeq: r.LastSeq, CreatedAt: r.CreatedAt,
	}
}

func mapMessage(r chatstore.ChatMessage) model.Message {
	return model.Message{
		ID: r.ID, RoomID: r.RoomID, Seq: r.Seq, AuthorID: r.AuthorID, ClientID: r.ClientID, Text: r.Body,
		Status: r.Status, HoldReason: deref(r.HoldReason), EscalationID: r.EscalationID, LegalHold: r.LegalHold,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, PurgedAt: r.PurgedAt,
	}
}

func mapMessages(rows []chatstore.ChatMessage) []model.Message {
	out := make([]model.Message, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapMessage(r))
	}
	return out
}
