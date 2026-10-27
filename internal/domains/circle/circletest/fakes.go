// Package circletest holds in-memory fakes of the circle ports for unit tests.
package circletest

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/port"
)

// Timer is a recorded platform timer.
type Timer struct {
	Kind      string
	SubjectID uuid.UUID
	DueAt     time.Time
	Cancelled bool
}

type state struct {
	requests      map[uuid.UUID]model.TeaRequest
	matches       map[uuid.UUID]model.Match
	acquaintances map[[2]uuid.UUID]model.Acquaintance
	events        []model.Event
	timers        []Timer
}

func (s *state) clone() *state {
	acq := maps.Clone(s.acquaintances)
	return &state{
		requests:      maps.Clone(s.requests),
		matches:       maps.Clone(s.matches),
		acquaintances: acq,
		events:        slices.Clone(s.events),
		timers:        slices.Clone(s.timers),
	}
}

// MemoryStore is an in-memory port.Store.
type MemoryStore struct {
	mu     sync.Mutex
	st     *state
	FailOn func(method string) error
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{st: &state{
		requests:      map[uuid.UUID]model.TeaRequest{},
		matches:       map[uuid.UUID]model.Match{},
		acquaintances: map[[2]uuid.UUID]model.Acquaintance{},
	}}
}

// InTx implements port.Store.
func (m *MemoryStore) InTx(_ context.Context, fn func(tx port.Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	work := m.st.clone()
	if err := fn(&memoryTx{st: work, failOn: m.FailOn}); err != nil {
		return err
	}
	m.st = work
	return nil
}

// Tx returns a port.Tx bound to a new transaction and a commit function.
func (m *MemoryStore) Tx() (port.Tx, func()) {
	m.mu.Lock()
	work := m.st.clone()
	return &memoryTx{st: work, failOn: m.FailOn}, func() {
		m.st = work
		m.mu.Unlock()
	}
}

// Events returns committed outbox events.
func (m *MemoryStore) Events() []model.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.st.events)
}

// EventsOfType returns committed events of type t.
func (m *MemoryStore) EventsOfType(t string) []model.Event {
	var out []model.Event
	for _, e := range m.Events() {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// Deliveries returns committed notify.DeliveryRequested payloads.
func (m *MemoryStore) Deliveries() []model.DeliveryRequestedData {
	var out []model.DeliveryRequestedData
	for _, e := range m.EventsOfType(model.EventDeliveryRequested) {
		out = append(out, e.Data.(model.DeliveryRequestedData))
	}
	return out
}

// ActiveTimers returns non-cancelled timers.
func (m *MemoryStore) ActiveTimers() []Timer {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Timer
	for _, t := range m.st.timers {
		if !t.Cancelled {
			out = append(out, t)
		}
	}
	return out
}

// Request returns a committed request.
func (m *MemoryStore) Request(id uuid.UUID) (model.TeaRequest, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.st.requests[id]
	return r, ok
}

// Match returns a committed match.
func (m *MemoryStore) Match(id uuid.UUID) (model.Match, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.st.matches[id]
	return r, ok
}

type memoryTx struct {
	st     *state
	failOn func(string) error
}

func (t *memoryTx) fail(method string) error {
	if t.failOn != nil {
		return t.failOn(method)
	}
	return nil
}

// InsertTeaRequest implements port.Tx.
func (t *memoryTx) InsertTeaRequest(_ context.Context, r model.TeaRequest) (model.TeaRequest, bool, error) {
	if err := t.fail("InsertTeaRequest"); err != nil {
		return model.TeaRequest{}, false, err
	}
	for _, existing := range t.st.requests {
		if existing.MemberID == r.MemberID && existing.IdempotencyKey == r.IdempotencyKey {
			return model.TeaRequest{}, false, nil
		}
	}
	for _, existing := range t.st.requests {
		if existing.MemberID == r.MemberID && existing.Status == model.RequestOpen {
			return model.TeaRequest{}, false, model.ErrConflict
		}
	}
	t.st.requests[r.ID] = r
	return r, true, nil
}

// GetTeaRequestByIdempotencyKey implements port.Tx.
func (t *memoryTx) GetTeaRequestByIdempotencyKey(_ context.Context, memberID, key uuid.UUID) (model.TeaRequest, error) {
	if err := t.fail("GetTeaRequestByIdempotencyKey"); err != nil {
		return model.TeaRequest{}, err
	}
	for _, r := range t.st.requests {
		if r.MemberID == memberID && r.IdempotencyKey == key {
			return r, nil
		}
	}
	return model.TeaRequest{}, model.ErrNotFound
}

// GetTeaRequest implements port.Tx.
func (t *memoryTx) GetTeaRequest(_ context.Context, id uuid.UUID, _ bool) (model.TeaRequest, error) {
	if err := t.fail("GetTeaRequest"); err != nil {
		return model.TeaRequest{}, err
	}
	r, ok := t.st.requests[id]
	if !ok {
		return model.TeaRequest{}, model.ErrNotFound
	}
	return r, nil
}

// ListTeaRequestsByMember implements port.Tx.
func (t *memoryTx) ListTeaRequestsByMember(_ context.Context, memberID uuid.UUID, limit int32) ([]model.TeaRequest, error) {
	var out []model.TeaRequest
	for _, r := range t.st.requests {
		if r.MemberID == memberID {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b model.TeaRequest) int {
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return 1
		}
		return 0
	})
	if int(limit) < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// ListOpenTeaRequests implements port.Tx.
func (t *memoryTx) ListOpenTeaRequests(_ context.Context, now time.Time) ([]model.TeaRequest, error) {
	var out []model.TeaRequest
	for _, r := range t.st.requests {
		if r.Status == model.RequestOpen && r.ExpiresAt.After(now) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b model.TeaRequest) int {
		if a.CreatedAt.Before(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.After(b.CreatedAt) {
			return 1
		}
		return bytesCmp(a.ID, b.ID)
	})
	return out, nil
}

// MarkTeaRequestMatched implements port.Tx.
func (t *memoryTx) MarkTeaRequestMatched(_ context.Context, id uuid.UUID) (model.TeaRequest, bool, error) {
	r, ok := t.st.requests[id]
	if !ok || r.Status != model.RequestOpen {
		return model.TeaRequest{}, false, nil
	}
	r.Status = model.RequestMatched
	t.st.requests[id] = r
	return r, true, nil
}

// CancelTeaRequest implements port.Tx.
func (t *memoryTx) CancelTeaRequest(_ context.Context, id, memberID uuid.UUID) (model.TeaRequest, bool, error) {
	r, ok := t.st.requests[id]
	if !ok || r.MemberID != memberID || r.Status != model.RequestOpen {
		return model.TeaRequest{}, false, nil
	}
	r.Status = model.RequestCancelled
	t.st.requests[id] = r
	return r, true, nil
}

// ExpireTeaRequest implements port.Tx.
func (t *memoryTx) ExpireTeaRequest(_ context.Context, id uuid.UUID) (model.TeaRequest, bool, error) {
	r, ok := t.st.requests[id]
	if !ok || r.Status != model.RequestOpen {
		return model.TeaRequest{}, false, nil
	}
	r.Status = model.RequestExpired
	t.st.requests[id] = r
	return r, true, nil
}

// InsertMatch implements port.Tx.
func (t *memoryTx) InsertMatch(_ context.Context, m model.Match) (model.Match, error) {
	if err := t.fail("InsertMatch"); err != nil {
		return model.Match{}, err
	}
	t.st.matches[m.ID] = m
	return m, nil
}

// GetMatch implements port.Tx.
func (t *memoryTx) GetMatch(_ context.Context, id uuid.UUID, _ bool) (model.Match, error) {
	m, ok := t.st.matches[id]
	if !ok {
		return model.Match{}, model.ErrNotFound
	}
	return m, nil
}

// ListMatchesForMember implements port.Tx.
func (t *memoryTx) ListMatchesForMember(_ context.Context, memberID uuid.UUID, limit int32) ([]model.Match, error) {
	var out []model.Match
	for _, m := range t.st.matches {
		if m.MemberA == memberID || m.MemberB == memberID {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b model.Match) int {
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return 1
		}
		return 0
	})
	if int(limit) < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// PairMatchedSince implements port.Tx.
func (t *memoryTx) PairMatchedSince(_ context.Context, memberA, memberB uuid.UUID, since time.Time) (bool, error) {
	for _, m := range t.st.matches {
		if m.MemberA == memberA && m.MemberB == memberB && !m.CreatedAt.Before(since) {
			return true, nil
		}
	}
	return false, nil
}

// UpdateMatchAnswers implements port.Tx.
func (t *memoryTx) UpdateMatchAnswers(_ context.Context, id uuid.UUID, answerA, answerB *bool, status string, askMetAt *time.Time) (model.Match, error) {
	m, ok := t.st.matches[id]
	if !ok {
		return model.Match{}, model.ErrNotFound
	}
	m.AnswerA, m.AnswerB, m.Status, m.AskMetAt = answerA, answerB, status, askMetAt
	t.st.matches[id] = m
	return m, nil
}

// UpdateMatchMet implements port.Tx.
func (t *memoryTx) UpdateMatchMet(_ context.Context, id uuid.UUID, metA, metB *bool, status string) (model.Match, error) {
	m, ok := t.st.matches[id]
	if !ok {
		return model.Match{}, model.ErrNotFound
	}
	m.MetA, m.MetB, m.Status = metA, metB, status
	t.st.matches[id] = m
	return m, nil
}

// SetMatchRoomID implements port.Tx.
func (t *memoryTx) SetMatchRoomID(_ context.Context, id, roomID uuid.UUID) (model.Match, bool, error) {
	m, ok := t.st.matches[id]
	if !ok || m.RoomID != nil {
		return model.Match{}, false, nil
	}
	rid := roomID
	m.RoomID = &rid
	t.st.matches[id] = m
	return m, true, nil
}

// SetMatchAskMetAt implements port.Tx.
func (t *memoryTx) SetMatchAskMetAt(_ context.Context, id uuid.UUID, at time.Time) (model.Match, bool, error) {
	m, ok := t.st.matches[id]
	if !ok || m.AskMetAt != nil {
		return model.Match{}, false, nil
	}
	m.AskMetAt = &at
	t.st.matches[id] = m
	return m, true, nil
}

// InsertAcquaintance implements port.Tx.
func (t *memoryTx) InsertAcquaintance(_ context.Context, memberA, memberB uuid.UUID, since time.Time) (model.Acquaintance, bool, error) {
	key := [2]uuid.UUID{memberA, memberB}
	if existing, ok := t.st.acquaintances[key]; ok {
		return existing, false, nil
	}
	a := model.Acquaintance{MemberA: memberA, MemberB: memberB, Since: since}
	t.st.acquaintances[key] = a
	return a, true, nil
}

// GetAcquaintance implements port.Tx.
func (t *memoryTx) GetAcquaintance(_ context.Context, memberA, memberB uuid.UUID) (model.Acquaintance, error) {
	a, ok := t.st.acquaintances[[2]uuid.UUID{memberA, memberB}]
	if !ok {
		return model.Acquaintance{}, model.ErrNotFound
	}
	return a, nil
}

// ListAcquaintancesForMember implements port.Tx.
func (t *memoryTx) ListAcquaintancesForMember(_ context.Context, memberID uuid.UUID) ([]model.Acquaintance, error) {
	var out []model.Acquaintance
	for _, a := range t.st.acquaintances {
		if a.MemberA == memberID || a.MemberB == memberID {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b model.Acquaintance) int {
		if a.Since.After(b.Since) {
			return -1
		}
		if a.Since.Before(b.Since) {
			return 1
		}
		return 0
	})
	return out, nil
}

// Publish implements port.Tx.
func (t *memoryTx) Publish(_ context.Context, _ time.Time, e model.Event) error {
	if err := t.fail("Publish:" + e.Type); err != nil {
		return err
	}
	t.st.events = append(t.st.events, e)
	return nil
}

func (t *memoryTx) schedule(kind string, subject uuid.UUID, dueAt time.Time) (bool, error) {
	for _, tm := range t.st.timers {
		if !tm.Cancelled && tm.Kind == kind && tm.SubjectID == subject {
			return false, nil
		}
	}
	t.st.timers = append(t.st.timers, Timer{Kind: kind, SubjectID: subject, DueAt: dueAt})
	return true, nil
}

func (t *memoryTx) cancel(kind string, subject uuid.UUID) (int64, error) {
	var n int64
	for i := range t.st.timers {
		tm := &t.st.timers[i]
		if !tm.Cancelled && tm.Kind == kind && tm.SubjectID == subject {
			tm.Cancelled = true
			n++
		}
	}
	return n, nil
}

// ScheduleMatchRun implements port.Tx.
func (t *memoryTx) ScheduleMatchRun(_ context.Context, dueAt time.Time) (bool, error) {
	return t.schedule(model.TimerMatchRun, model.MatchRunSubject, dueAt)
}

// CancelMatchRun implements port.Tx.
func (t *memoryTx) CancelMatchRun(_ context.Context, _ time.Time) (int64, error) {
	return t.cancel(model.TimerMatchRun, model.MatchRunSubject)
}

// ScheduleRequestExpire implements port.Tx.
func (t *memoryTx) ScheduleRequestExpire(_ context.Context, requestID uuid.UUID, dueAt time.Time) (bool, error) {
	return t.schedule(model.TimerRequestExpire, requestID, dueAt)
}

// CancelRequestExpire implements port.Tx.
func (t *memoryTx) CancelRequestExpire(_ context.Context, requestID uuid.UUID, _ time.Time) (int64, error) {
	return t.cancel(model.TimerRequestExpire, requestID)
}

// ScheduleAskMet implements port.Tx.
func (t *memoryTx) ScheduleAskMet(_ context.Context, matchID uuid.UUID, dueAt time.Time) (bool, error) {
	return t.schedule(model.TimerAskMet, matchID, dueAt)
}

// CancelAskMet implements port.Tx.
func (t *memoryTx) CancelAskMet(_ context.Context, matchID uuid.UUID, _ time.Time) (int64, error) {
	return t.cancel(model.TimerAskMet, matchID)
}

func bytesCmp(a, b uuid.UUID) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// FakeMembers implements MemberInfo and Blocks for tests.
type FakeMembers struct {
	mu       sync.Mutex
	profiles map[uuid.UUID]model.Profile
	blocks   map[[2]uuid.UUID]struct{}
	Fail     error
}

// NewFakeMembers returns an empty FakeMembers.
func NewFakeMembers() *FakeMembers {
	return &FakeMembers{profiles: map[uuid.UUID]model.Profile{}, blocks: map[[2]uuid.UUID]struct{}{}}
}

// Put stores a profile.
func (f *FakeMembers) Put(id uuid.UUID, p model.Profile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles[id] = p
}

// Block records a mutual block between a and b.
func (f *FakeMembers) Block(a, b uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	lo, hi := model.CanonicalPair(a, b)
	f.blocks[[2]uuid.UUID{lo, hi}] = struct{}{}
}

// Profiles implements port.MemberInfo.
func (f *FakeMembers) Profiles(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]model.Profile, error) {
	if f.Fail != nil {
		return nil, f.Fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[uuid.UUID]model.Profile{}
	for _, id := range ids {
		if p, ok := f.profiles[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

// IsBlocked implements port.Blocks.
func (f *FakeMembers) IsBlocked(_ context.Context, a, b uuid.UUID) (bool, error) {
	if f.Fail != nil {
		return false, f.Fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	lo, hi := model.CanonicalPair(a, b)
	_, ok := f.blocks[[2]uuid.UUID{lo, hi}]
	return ok, nil
}

// Clock is a fixed clock for tests.
type Clock struct{ T time.Time }

// Now returns the fixed time.
func (c *Clock) Now() time.Time { return c.T }

var (
	_ port.Store      = (*MemoryStore)(nil)
	_ port.Tx         = (*memoryTx)(nil)
	_ port.MemberInfo = (*FakeMembers)(nil)
	_ port.Blocks     = (*FakeMembers)(nil)
)
