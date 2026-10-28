package chattest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
)

// MemStore is an in-memory port.Store for service and handler tests. It
// mirrors the semantics of the SQL in db/queries/chat_*.sql that the service
// relies on (conflict-free inserts, left members kept, purge exemptions) and
// rolls a transaction back when fn returns an error, so a test sees exactly
// what a committed transaction would leave. Transactions are serialised by
// one mutex; Lock is therefore a no-op. Store logic itself (locking, SQL)
// is covered by the PostgreSQL integration tests in package adapter.
//
// FailOn makes one Tx method return an error, for failure-path tests.
type MemStore struct {
	mu    sync.Mutex
	state memState
	fail  map[string]error
}

// Timer is a platform timer recorded by MemStore.
type Timer struct {
	Kind      string
	SubjectID uuid.UUID
	DedupKey  string
	DueAt     time.Time
	Cancelled bool
}

// Report is a chat report recorded by MemStore.
type Report struct {
	ID, MessageID, RoomID, ReporterID uuid.UUID
	Reason                            string
	Closed                            bool
}

type memberKey struct{ room, member uuid.UUID }

type memMember struct {
	model.RoomMember
	connectedUntil *time.Time
}

type memMentor struct {
	assignedAt time.Time
	removed    bool
}

type memState struct {
	rooms        map[uuid.UUID]model.Room
	members      map[memberKey]memMember
	restrictions map[memberKey]model.Restriction
	mentors      map[memberKey]memMentor
	messages     map[uuid.UUID]model.Message
	reports      []Report
	processed    map[string]bool
	events       []model.Event
	timers       []Timer
	audit        []model.AuditEntry
	locks        []string
}

func (s memState) clone() memState {
	return memState{
		rooms:        cloneMap(s.rooms),
		members:      cloneMap(s.members),
		restrictions: cloneMap(s.restrictions),
		mentors:      cloneMap(s.mentors),
		messages:     cloneMap(s.messages),
		reports:      slices.Clone(s.reports),
		processed:    cloneMap(s.processed),
		events:       slices.Clone(s.events),
		timers:       slices.Clone(s.timers),
		audit:        slices.Clone(s.audit),
		locks:        slices.Clone(s.locks),
	}
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{state: memState{}.clone(), fail: map[string]error{}}
}

// FailOn makes the Tx method named method (for example "InsertMessage")
// return err until cleared with a nil err.
func (s *MemStore) FailOn(method string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.fail, method)
		return
	}
	s.fail[method] = err
}

// InTx implements port.Store.
func (s *MemStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("memstore: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail["InTx"]; err != nil {
		return err
	}
	saved := s.state.clone()
	if err := fn(&memTx{s: s}); err != nil {
		s.state = saved
		return err
	}
	return nil
}

// Events returns the committed outbox events.
func (s *MemStore) Events() []model.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.state.events)
}

// EventsOf returns the committed outbox events of one type.
func (s *MemStore) EventsOf(eventType string) []model.Event {
	var out []model.Event
	for _, e := range s.Events() {
		if e.Type == eventType {
			out = append(out, e)
		}
	}
	return out
}

// Timers returns the committed timers.
func (s *MemStore) Timers() []Timer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.state.timers)
}

// ActiveTimers returns the committed, not cancelled timers of kind.
func (s *MemStore) ActiveTimers(kind string) []Timer {
	var out []Timer
	for _, t := range s.Timers() {
		if t.Kind == kind && !t.Cancelled {
			out = append(out, t)
		}
	}
	return out
}

// Audit returns the committed audit entries.
func (s *MemStore) Audit() []model.AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.state.audit)
}

// Reports returns the committed reports.
func (s *MemStore) Reports() []Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.state.reports)
}

// Locks returns the lock keys taken by committed transactions, in order.
func (s *MemStore) Locks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.state.locks)
}

// Message returns a committed message.
func (s *MemStore) Message(id uuid.UUID) (model.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.state.messages[id]
	return m, ok
}

// Messages returns the committed messages of a room, by seq.
func (s *MemStore) Messages(roomID uuid.UUID) []model.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Message
	for _, m := range s.state.messages {
		if m.RoomID == roomID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// Room returns a committed room.
func (s *MemStore) Room(id uuid.UUID) (model.Room, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.state.rooms[id]
	return r, ok
}

// Member returns a committed room membership (also a left one).
func (s *MemStore) Member(roomID, memberID uuid.UUID) (model.RoomMember, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.state.members[memberKey{roomID, memberID}]
	return m.RoomMember, ok
}

// Restriction returns a member's committed restriction in a room.
func (s *MemStore) Restriction(roomID, memberID uuid.UUID) model.Restriction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.restrictions[memberKey{roomID, memberID}]
}

// SetCreatedAt backdates a committed message (retention, slow mode tests).
func (s *MemStore) SetCreatedAt(id uuid.UUID, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.state.messages[id]
	m.CreatedAt = at
	s.state.messages[id] = m
}

// memTx is one MemStore transaction; the store mutex is held by InTx.
type memTx struct{ s *MemStore }

var _ port.Tx = (*memTx)(nil)

func (t *memTx) st() *memState { return &t.s.state }

func (t *memTx) failed(method string) error { return t.s.fail[method] }

// Lock implements port.Tx.
func (t *memTx) Lock(_ context.Context, scope string, id uuid.UUID) error {
	if err := t.failed("Lock"); err != nil {
		return err
	}
	t.st().locks = append(t.st().locks, scope+"|"+id.String())
	return nil
}

// InsertGroupRoom implements port.Tx.
func (t *memTx) InsertGroupRoom(_ context.Context, r model.Room) (model.Room, bool, error) {
	if err := t.failed("InsertGroupRoom"); err != nil {
		return model.Room{}, false, err
	}
	for _, x := range t.st().rooms {
		if x.Kind != r.Kind {
			continue
		}
		if r.Kind == model.KindDistrict && x.DistrictID != nil && r.DistrictID != nil && *x.DistrictID == *r.DistrictID {
			return model.Room{}, false, nil
		}
		if r.Kind == model.KindTopic && x.TopicCode == r.TopicCode {
			return model.Room{}, false, nil
		}
	}
	t.st().rooms[r.ID] = r
	return r, true, nil
}

func (t *memTx) findRoom(match func(model.Room) bool) (model.Room, error) {
	for _, r := range t.st().rooms {
		if match(r) {
			return r, nil
		}
	}
	return model.Room{}, model.ErrNotFound
}

// GetDistrictRoom implements port.Tx.
func (t *memTx) GetDistrictRoom(_ context.Context, districtID uuid.UUID) (model.Room, error) {
	if err := t.failed("GetDistrictRoom"); err != nil {
		return model.Room{}, err
	}
	return t.findRoom(func(r model.Room) bool {
		return r.Kind == model.KindDistrict && r.DistrictID != nil && *r.DistrictID == districtID
	})
}

// GetTopicRoom implements port.Tx.
func (t *memTx) GetTopicRoom(_ context.Context, code string) (model.Room, error) {
	if err := t.failed("GetTopicRoom"); err != nil {
		return model.Room{}, err
	}
	return t.findRoom(func(r model.Room) bool { return r.Kind == model.KindTopic && r.TopicCode == code })
}

func sameDirect(r model.Room, origin string, originID, low, high uuid.UUID) bool {
	return r.Kind == model.KindDirect && r.Origin == origin && r.OriginID != nil && *r.OriginID == originID &&
		r.MemberLow != nil && *r.MemberLow == low && r.MemberHigh != nil && *r.MemberHigh == high
}

// InsertDirectRoom implements port.Tx.
func (t *memTx) InsertDirectRoom(_ context.Context, r model.Room) (model.Room, bool, error) {
	if err := t.failed("InsertDirectRoom"); err != nil {
		return model.Room{}, false, err
	}
	if _, err := t.findRoom(func(x model.Room) bool {
		return sameDirect(x, r.Origin, *r.OriginID, *r.MemberLow, *r.MemberHigh)
	}); err == nil {
		return model.Room{}, false, nil
	}
	t.st().rooms[r.ID] = r
	return r, true, nil
}

// GetDirectRoom implements port.Tx.
func (t *memTx) GetDirectRoom(_ context.Context, origin string, originID, low, high uuid.UUID) (model.Room, error) {
	if err := t.failed("GetDirectRoom"); err != nil {
		return model.Room{}, err
	}
	return t.findRoom(func(r model.Room) bool { return sameDirect(r, origin, originID, low, high) })
}

// GetRoom implements port.Tx.
func (t *memTx) GetRoom(_ context.Context, id uuid.UUID, _ bool) (model.Room, error) {
	if err := t.failed("GetRoom"); err != nil {
		return model.Room{}, err
	}
	r, ok := t.st().rooms[id]
	if !ok {
		return model.Room{}, model.ErrNotFound
	}
	return r, nil
}

// NextSeq implements port.Tx.
func (t *memTx) NextSeq(_ context.Context, roomID uuid.UUID) (int64, error) {
	if err := t.failed("NextSeq"); err != nil {
		return 0, err
	}
	r, ok := t.st().rooms[roomID]
	if !ok {
		return 0, model.ErrNotFound
	}
	r.LastSeq++
	t.st().rooms[roomID] = r
	return r.LastSeq, nil
}

// SetSlowMode implements port.Tx.
func (t *memTx) SetSlowMode(_ context.Context, roomID uuid.UUID, seconds int) error {
	if err := t.failed("SetSlowMode"); err != nil {
		return err
	}
	r := t.st().rooms[roomID]
	r.SlowModeSeconds = seconds
	t.st().rooms[roomID] = r
	return nil
}

func sortRooms(rs []model.Room) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Kind != rs[j].Kind {
			return rs[i].Kind < rs[j].Kind
		}
		if !rs[i].CreatedAt.Equal(rs[j].CreatedAt) {
			return rs[i].CreatedAt.Before(rs[j].CreatedAt)
		}
		return strings.Compare(rs[i].ID.String(), rs[j].ID.String()) < 0
	})
}

// ListJoinableGroupRooms implements port.Tx.
func (t *memTx) ListJoinableGroupRooms(_ context.Context, districtID *uuid.UUID) ([]model.Room, error) {
	if err := t.failed("ListJoinableGroupRooms"); err != nil {
		return nil, err
	}
	var out []model.Room
	for _, r := range t.st().rooms {
		if r.Kind == model.KindTopic || (r.Kind == model.KindDistrict && districtID != nil && r.DistrictID != nil && *r.DistrictID == *districtID) {
			out = append(out, r)
		}
	}
	sortRooms(out)
	return out, nil
}

// ListMemberRooms implements port.Tx.
func (t *memTx) ListMemberRooms(_ context.Context, memberID uuid.UUID) ([]model.MemberRoom, error) {
	if err := t.failed("ListMemberRooms"); err != nil {
		return nil, err
	}
	var rooms []model.Room
	muted := map[uuid.UUID]bool{}
	for k, m := range t.st().members {
		if k.member == memberID && m.Active() {
			rooms = append(rooms, t.st().rooms[k.room])
			muted[k.room] = m.Muted
		}
	}
	sortRooms(rooms)
	out := make([]model.MemberRoom, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, model.MemberRoom{Room: r, Muted: muted[r.ID]})
	}
	return out, nil
}

// ListStaffRooms implements port.Tx.
func (t *memTx) ListStaffRooms(_ context.Context) ([]model.StaffRoomSummary, error) {
	if err := t.failed("ListStaffRooms"); err != nil {
		return nil, err
	}
	var out []model.StaffRoomSummary
	for _, r := range t.st().rooms {
		if !r.Group() {
			continue
		}
		sum := model.StaffRoomSummary{ID: r.ID, Kind: r.Kind, TitleKey: r.TitleKey, MentorIDs: []uuid.UUID{}}
		for k, m := range t.st().members {
			if k.room == r.ID && m.Active() {
				sum.MemberCount++
			}
		}
		type am struct {
			id uuid.UUID
			at time.Time
		}
		var ms []am
		for k, m := range t.st().mentors {
			if k.room == r.ID && !m.removed {
				ms = append(ms, am{k.member, m.assignedAt})
			}
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i].at.Before(ms[j].at) })
		for _, m := range ms {
			sum.MentorIDs = append(sum.MentorIDs, m.id)
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].TitleKey < out[j].TitleKey
	})
	return out, nil
}

// GetRoomMember implements port.Tx.
func (t *memTx) GetRoomMember(_ context.Context, roomID, memberID uuid.UUID) (model.RoomMember, error) {
	if err := t.failed("GetRoomMember"); err != nil {
		return model.RoomMember{}, err
	}
	m, ok := t.st().members[memberKey{roomID, memberID}]
	if !ok {
		return model.RoomMember{}, model.ErrNotFound
	}
	return m.RoomMember, nil
}

// JoinRoom implements port.Tx.
func (t *memTx) JoinRoom(_ context.Context, roomID, memberID uuid.UUID, at time.Time) error {
	if err := t.failed("JoinRoom"); err != nil {
		return err
	}
	k := memberKey{roomID, memberID}
	m, ok := t.st().members[k]
	switch {
	case !ok:
		m = memMember{RoomMember: model.RoomMember{RoomID: roomID, MemberID: memberID, JoinedAt: at}}
	case m.LeftAt != nil:
		// Re-join keeps the earlier mute flag (JoinRoom query).
		m.LeftAt, m.JoinedAt = nil, at
	}
	t.st().members[k] = m
	return nil
}

// AddRoomMemberIfAbsent implements port.Tx.
func (t *memTx) AddRoomMemberIfAbsent(_ context.Context, roomID, memberID uuid.UUID, at time.Time) error {
	if err := t.failed("AddRoomMemberIfAbsent"); err != nil {
		return err
	}
	k := memberKey{roomID, memberID}
	if _, ok := t.st().members[k]; !ok {
		t.st().members[k] = memMember{RoomMember: model.RoomMember{RoomID: roomID, MemberID: memberID, JoinedAt: at}}
	}
	return nil
}

// LeaveRoom implements port.Tx.
func (t *memTx) LeaveRoom(_ context.Context, roomID, memberID uuid.UUID, at time.Time) (bool, error) {
	if err := t.failed("LeaveRoom"); err != nil {
		return false, err
	}
	k := memberKey{roomID, memberID}
	m, ok := t.st().members[k]
	if !ok || m.LeftAt != nil {
		return false, nil
	}
	m.LeftAt = &at
	t.st().members[k] = m
	return true, nil
}

// SetMute implements port.Tx.
func (t *memTx) SetMute(_ context.Context, roomID, memberID uuid.UUID, muted bool) (bool, error) {
	if err := t.failed("SetMute"); err != nil {
		return false, err
	}
	k := memberKey{roomID, memberID}
	m, ok := t.st().members[k]
	if !ok || m.LeftAt != nil {
		return false, nil
	}
	m.Muted = muted
	t.st().members[k] = m
	return true, nil
}

// TouchPresence implements port.Tx.
func (t *memTx) TouchPresence(_ context.Context, roomID, memberID uuid.UUID, until time.Time) error {
	if err := t.failed("TouchPresence"); err != nil {
		return err
	}
	k := memberKey{roomID, memberID}
	if m, ok := t.st().members[k]; ok && m.LeftAt == nil {
		m.connectedUntil = &until
		t.st().members[k] = m
	}
	return nil
}

// ListPushCandidates implements port.Tx.
func (t *memTx) ListPushCandidates(_ context.Context, roomID, authorID uuid.UUID, now, pushedBefore time.Time) ([]uuid.UUID, error) {
	if err := t.failed("ListPushCandidates"); err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for k, m := range t.st().members {
		if k.room != roomID || m.LeftAt != nil || k.member == authorID || m.Muted {
			continue
		}
		if m.connectedUntil != nil && m.connectedUntil.After(now) {
			continue
		}
		if m.LastPushAt != nil && m.LastPushAt.After(pushedBefore) {
			continue
		}
		out = append(out, k.member)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// MarkPushed implements port.Tx.
func (t *memTx) MarkPushed(_ context.Context, roomID, memberID uuid.UUID, at time.Time) error {
	if err := t.failed("MarkPushed"); err != nil {
		return err
	}
	k := memberKey{roomID, memberID}
	if m, ok := t.st().members[k]; ok {
		m.LastPushAt = &at
		t.st().members[k] = m
	}
	return nil
}

// GetRestriction implements port.Tx.
func (t *memTx) GetRestriction(_ context.Context, roomID, memberID uuid.UUID) (model.Restriction, error) {
	if err := t.failed("GetRestriction"); err != nil {
		return model.Restriction{}, err
	}
	return t.st().restrictions[memberKey{roomID, memberID}], nil
}

// SetBan implements port.Tx.
func (t *memTx) SetBan(_ context.Context, roomID, memberID uuid.UUID, at *time.Time, _ time.Time) error {
	if err := t.failed("SetBan"); err != nil {
		return err
	}
	k := memberKey{roomID, memberID}
	r := t.st().restrictions[k]
	r.BannedAt = at
	t.st().restrictions[k] = r
	return nil
}

// SetMutedUntil implements port.Tx.
func (t *memTx) SetMutedUntil(_ context.Context, roomID, memberID uuid.UUID, until, _ time.Time) error {
	if err := t.failed("SetMutedUntil"); err != nil {
		return err
	}
	k := memberKey{roomID, memberID}
	r := t.st().restrictions[k]
	r.MutedUntil = &until
	t.st().restrictions[k] = r
	return nil
}

// AssignMentor implements port.Tx.
func (t *memTx) AssignMentor(_ context.Context, roomID, memberID, _ uuid.UUID, at time.Time) error {
	if err := t.failed("AssignMentor"); err != nil {
		return err
	}
	t.st().mentors[memberKey{roomID, memberID}] = memMentor{assignedAt: at}
	return nil
}

// RemoveMentor implements port.Tx.
func (t *memTx) RemoveMentor(_ context.Context, roomID, memberID uuid.UUID, _ time.Time) (bool, error) {
	if err := t.failed("RemoveMentor"); err != nil {
		return false, err
	}
	k := memberKey{roomID, memberID}
	m, ok := t.st().mentors[k]
	if !ok || m.removed {
		return false, nil
	}
	m.removed = true
	t.st().mentors[k] = m
	return true, nil
}

// RemoveMemberMentorships implements port.Tx.
func (t *memTx) RemoveMemberMentorships(_ context.Context, memberID uuid.UUID, _ time.Time) ([]uuid.UUID, error) {
	if err := t.failed("RemoveMemberMentorships"); err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for k, m := range t.st().mentors {
		if k.member == memberID && !m.removed {
			m.removed = true
			t.st().mentors[k] = m
			out = append(out, k.room)
		}
	}
	return out, nil
}

// IsRoomMentor implements port.Tx.
func (t *memTx) IsRoomMentor(_ context.Context, roomID, memberID uuid.UUID) (bool, error) {
	if err := t.failed("IsRoomMentor"); err != nil {
		return false, err
	}
	m, ok := t.st().mentors[memberKey{roomID, memberID}]
	return ok && !m.removed, nil
}

// InsertMessage implements port.Tx.
func (t *memTx) InsertMessage(_ context.Context, m model.Message) (model.Message, bool, error) {
	if err := t.failed("InsertMessage"); err != nil {
		return model.Message{}, false, err
	}
	for _, x := range t.st().messages {
		if x.AuthorID == m.AuthorID && x.ClientID == m.ClientID {
			return model.Message{}, false, nil
		}
	}
	m.UpdatedAt = m.CreatedAt
	t.st().messages[m.ID] = m
	return m, true, nil
}

// GetMessageByClientID implements port.Tx.
func (t *memTx) GetMessageByClientID(_ context.Context, authorID, clientID uuid.UUID) (model.Message, error) {
	if err := t.failed("GetMessageByClientID"); err != nil {
		return model.Message{}, err
	}
	for _, x := range t.st().messages {
		if x.AuthorID == authorID && x.ClientID == clientID {
			return x, nil
		}
	}
	return model.Message{}, model.ErrNotFound
}

// GetMessage implements port.Tx.
func (t *memTx) GetMessage(_ context.Context, id uuid.UUID, _ bool) (model.Message, error) {
	if err := t.failed("GetMessage"); err != nil {
		return model.Message{}, err
	}
	m, ok := t.st().messages[id]
	if !ok {
		return model.Message{}, model.ErrNotFound
	}
	return m, nil
}

func (t *memTx) roomMessages(roomID uuid.UUID) []model.Message {
	var out []model.Message
	for _, m := range t.st().messages {
		if m.RoomID == roomID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// ListMessagesAfter implements port.Tx.
func (t *memTx) ListMessagesAfter(_ context.Context, roomID uuid.UUID, after int64, limit int32) ([]model.Message, error) {
	if err := t.failed("ListMessagesAfter"); err != nil {
		return nil, err
	}
	var out []model.Message
	for _, m := range t.roomMessages(roomID) {
		if m.Seq > after && len(out) < int(limit) {
			out = append(out, m)
		}
	}
	return out, nil
}

// ListMessagesBefore implements port.Tx.
func (t *memTx) ListMessagesBefore(_ context.Context, roomID uuid.UUID, before int64, limit int32) ([]model.Message, error) {
	if err := t.failed("ListMessagesBefore"); err != nil {
		return nil, err
	}
	all := t.roomMessages(roomID)
	slices.Reverse(all)
	var out []model.Message
	for _, m := range all {
		if m.Seq < before && len(out) < int(limit) {
			out = append(out, m)
		}
	}
	return out, nil
}

// CountAuthorMessagesSince implements port.Tx.
func (t *memTx) CountAuthorMessagesSince(_ context.Context, authorID uuid.UUID, since time.Time) (int, error) {
	if err := t.failed("CountAuthorMessagesSince"); err != nil {
		return 0, err
	}
	n := 0
	for _, m := range t.st().messages {
		if m.AuthorID == authorID && m.CreatedAt.After(since) {
			n++
		}
	}
	return n, nil
}

// CountAuthorGroupMessages implements port.Tx.
func (t *memTx) CountAuthorGroupMessages(_ context.Context, authorID uuid.UUID, upTo int) (int, error) {
	if err := t.failed("CountAuthorGroupMessages"); err != nil {
		return 0, err
	}
	n := 0
	for _, m := range t.st().messages {
		if m.AuthorID == authorID && t.st().rooms[m.RoomID].Group() && n < upTo {
			n++
		}
	}
	return n, nil
}

// LastAuthorMessageAt implements port.Tx.
func (t *memTx) LastAuthorMessageAt(_ context.Context, roomID, authorID uuid.UUID) (*time.Time, error) {
	if err := t.failed("LastAuthorMessageAt"); err != nil {
		return nil, err
	}
	var last *time.Time
	for _, m := range t.st().messages {
		if m.RoomID == roomID && m.AuthorID == authorID && (last == nil || m.CreatedAt.After(*last)) {
			at := m.CreatedAt
			last = &at
		}
	}
	return last, nil
}

// SetMessageStatus implements port.Tx.
func (t *memTx) SetMessageStatus(_ context.Context, id uuid.UUID, status string, at time.Time) (model.Message, error) {
	if err := t.failed("SetMessageStatus"); err != nil {
		return model.Message{}, err
	}
	m, ok := t.st().messages[id]
	if !ok {
		return model.Message{}, model.ErrNotFound
	}
	m.Status, m.UpdatedAt = status, at
	t.st().messages[id] = m
	return m, nil
}

// SetMessageEscalation implements port.Tx.
func (t *memTx) SetMessageEscalation(_ context.Context, id, escalationID uuid.UUID) error {
	if err := t.failed("SetMessageEscalation"); err != nil {
		return err
	}
	m, ok := t.st().messages[id]
	if ok && m.EscalationID == nil {
		e := escalationID
		m.EscalationID = &e
		t.st().messages[id] = m
	}
	return nil
}

// SetLegalHold implements port.Tx.
func (t *memTx) SetLegalHold(_ context.Context, id uuid.UUID, on bool) (bool, error) {
	if err := t.failed("SetLegalHold"); err != nil {
		return false, err
	}
	m, ok := t.st().messages[id]
	if !ok {
		return false, nil
	}
	m.LegalHold = on
	t.st().messages[id] = m
	return true, nil
}

// PurgeExpired implements port.Tx.
func (t *memTx) PurgeExpired(_ context.Context, kinds []string, createdBefore, now time.Time, limit int32) (int64, error) {
	if err := t.failed("PurgeExpired"); err != nil {
		return 0, err
	}
	openReport := map[uuid.UUID]bool{}
	for _, r := range t.st().reports {
		if !r.Closed {
			openReport[r.MessageID] = true
		}
	}
	var due []model.Message
	for _, m := range t.st().messages {
		if m.PurgedAt == nil && m.CreatedAt.Before(createdBefore) && slices.Contains(kinds, t.st().rooms[m.RoomID].Kind) &&
			!m.LegalHold && !openReport[m.ID] {
			due = append(due, m)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].CreatedAt.Before(due[j].CreatedAt) })
	var n int64
	for _, m := range due {
		if n >= int64(limit) {
			break
		}
		at := now
		m.Text, m.PurgedAt, m.UpdatedAt = nil, &at, now
		t.st().messages[m.ID] = m
		n++
	}
	return n, nil
}

// InsertReport implements port.Tx.
func (t *memTx) InsertReport(_ context.Context, id, messageID, roomID, reporterID uuid.UUID, reason string, _ time.Time) (bool, error) {
	if err := t.failed("InsertReport"); err != nil {
		return false, err
	}
	for _, r := range t.st().reports {
		if r.MessageID == messageID && r.ReporterID == reporterID {
			return false, nil
		}
	}
	t.st().reports = append(t.st().reports, Report{ID: id, MessageID: messageID, RoomID: roomID, ReporterID: reporterID, Reason: reason})
	return true, nil
}

// CloseReports implements port.Tx.
func (t *memTx) CloseReports(_ context.Context, messageID uuid.UUID, _ time.Time) (int64, error) {
	if err := t.failed("CloseReports"); err != nil {
		return 0, err
	}
	var n int64
	for i, r := range t.st().reports {
		if r.MessageID == messageID && !r.Closed {
			t.st().reports[i].Closed = true
			n++
		}
	}
	return n, nil
}

// MarkProcessed implements port.Tx.
func (t *memTx) MarkProcessed(_ context.Context, consumer string, eventID uuid.UUID) (bool, error) {
	if err := t.failed("MarkProcessed"); err != nil {
		return false, err
	}
	k := consumer + "|" + eventID.String()
	if t.st().processed[k] {
		return false, nil
	}
	t.st().processed[k] = true
	return true, nil
}

// Publish implements port.Tx.
func (t *memTx) Publish(_ context.Context, _ time.Time, e model.Event) error {
	if err := t.failed("Publish"); err != nil {
		return err
	}
	t.st().events = append(t.st().events, e)
	return nil
}

// Schedule implements port.Tx.
func (t *memTx) Schedule(_ context.Context, kind string, subjectID uuid.UUID, dedupKey string, dueAt time.Time) (bool, error) {
	if err := t.failed("Schedule"); err != nil {
		return false, err
	}
	for _, x := range t.st().timers {
		if x.Kind == kind && x.DedupKey == dedupKey {
			return false, nil
		}
	}
	t.st().timers = append(t.st().timers, Timer{Kind: kind, SubjectID: subjectID, DedupKey: dedupKey, DueAt: dueAt})
	return true, nil
}

// CancelTimers implements port.Tx.
func (t *memTx) CancelTimers(_ context.Context, kind string, subjectID uuid.UUID, _ time.Time) error {
	if err := t.failed("CancelTimers"); err != nil {
		return err
	}
	for i, x := range t.st().timers {
		if x.Kind == kind && x.SubjectID == subjectID {
			t.st().timers[i].Cancelled = true
		}
	}
	return nil
}

// Audit implements port.Tx.
func (t *memTx) Audit(_ context.Context, e model.AuditEntry) error {
	if err := t.failed("Audit"); err != nil {
		return err
	}
	if e.ActorID == uuid.Nil || e.Action == "" {
		return errors.New("chattest: audit entry without actor or action")
	}
	t.st().audit = append(t.st().audit, e)
	return nil
}
