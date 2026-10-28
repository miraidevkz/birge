// Package wellbeingtest holds in-memory fakes for wellbeing unit tests.
package wellbeingtest

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/wellbeing/model"
	"birge/internal/domains/wellbeing/port"
)

// FakeSealer records Seal calls; ciphertext is a trivial transform for tests.
type FakeSealer struct {
	mu    sync.Mutex
	Calls []SealCall
	Err   error
}

// SealCall is one recorded Seal invocation (plaintext kept for assertions in tests only).
type SealCall struct {
	MemberID       uuid.UUID
	AssociatedData []byte
	PlaintextLen   int
}

// Seal implements port.Sealer.
func (f *FakeSealer) Seal(_ context.Context, memberID uuid.UUID, plaintext, associatedData []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, SealCall{
		MemberID:       memberID,
		AssociatedData: append([]byte(nil), associatedData...),
		PlaintextLen:   len(plaintext),
	})
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([]byte, len(plaintext)+1)
	out[0] = 'S'
	copy(out[1:], plaintext)
	return out, nil
}

// FakeCrisisOpener records OpenEscalation calls.
type FakeCrisisOpener struct {
	mu     sync.Mutex
	Calls  []CrisisCall
	NextID uuid.UUID
	Err    error
}

// CrisisCall is one recorded OpenEscalation invocation.
type CrisisCall struct {
	MemberID uuid.UUID
	Source   string
}

// OpenEscalation implements port.CrisisOpener.
func (f *FakeCrisisOpener) OpenEscalation(_ context.Context, memberID uuid.UUID, source string) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, CrisisCall{MemberID: memberID, Source: source})
	if f.Err != nil {
		return uuid.Nil, f.Err
	}
	id := f.NextID
	if id == uuid.Nil {
		id = uuid.Must(uuid.NewV7())
		f.NextID = id
	}
	return id, nil
}

// Len returns how many escalation opens were attempted.
func (f *FakeCrisisOpener) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

// MemoryStore is an in-memory port.Store for unit tests.
type MemoryStore struct {
	mu sync.Mutex

	schedules map[uuid.UUID]model.Schedule
	checkins  []model.Checkin
	practices []model.Practice
	events    []MemoryEvent
	timers    map[string]time.Time // kind|subject -> due
}

// MemoryEvent is a recorded outbox event.
type MemoryEvent struct {
	Type    string
	Version int
	Data    any
	At      time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		schedules: make(map[uuid.UUID]model.Schedule),
		timers:    make(map[string]time.Time),
	}
}

// InTx runs fn under a mutex.
func (s *MemoryStore) InTx(_ context.Context, fn func(tx port.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(&memoryTx{s: s})
}

// Events returns a copy of recorded events.
func (s *MemoryStore) Events() []MemoryEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]MemoryEvent, len(s.events))
	copy(out, s.events)
	return out
}

// EventCount returns how many events of type were recorded.
func (s *MemoryStore) EventCount(eventType string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.Type == eventType {
			n++
		}
	}
	return n
}

// TimerDue returns the due time for kind/subject, or false if none.
func (s *MemoryStore) TimerDue(kind string, subject uuid.UUID) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	due, ok := s.timers[timerKey(kind, subject)]
	return due, ok
}

// AddPractice seeds a practice row for ListPractices tests.
func (s *MemoryStore) AddPractice(p model.Practice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ID == uuid.Nil {
		p.ID = uuid.Must(uuid.NewV7())
	}
	s.practices = append(s.practices, p)
}

func timerKey(kind string, subject uuid.UUID) string {
	return kind + "|" + subject.String()
}

type memoryTx struct {
	s *MemoryStore
}

// UpsertSchedule inserts or replaces the member's reminder schedule.
func (t *memoryTx) UpsertSchedule(_ context.Context, s model.Schedule) (model.Schedule, error) {
	t.s.schedules[s.MemberID] = s
	return s, nil
}

// GetSchedule returns the member's schedule or model.ErrNotFound.
func (t *memoryTx) GetSchedule(_ context.Context, memberID uuid.UUID) (model.Schedule, error) {
	s, ok := t.s.schedules[memberID]
	if !ok {
		return model.Schedule{}, model.ErrNotFound
	}
	return s, nil
}

// InsertCheckin inserts c; inserted is false when the member/idempotency key already exists.
func (t *memoryTx) InsertCheckin(_ context.Context, c model.Checkin) (model.Checkin, bool, error) {
	for _, existing := range t.s.checkins {
		if existing.MemberID == c.MemberID && existing.IdempotencyKey == c.IdempotencyKey {
			return model.Checkin{}, false, nil
		}
	}
	if c.ID == uuid.Nil {
		c.ID = uuid.Must(uuid.NewV7())
	}
	t.s.checkins = append(t.s.checkins, c)
	return c, true, nil
}

// GetCheckinByIdempotencyKey returns the check-in for member/key or model.ErrNotFound.
func (t *memoryTx) GetCheckinByIdempotencyKey(_ context.Context, memberID, key uuid.UUID) (model.Checkin, error) {
	for _, c := range t.s.checkins {
		if c.MemberID == memberID && c.IdempotencyKey == key {
			return c, nil
		}
	}
	return model.Checkin{}, model.ErrNotFound
}

// ListCheckinHistory returns check-ins completed at or after since, oldest first.
func (t *memoryTx) ListCheckinHistory(_ context.Context, memberID uuid.UUID, since time.Time, limit int32) ([]model.Checkin, error) {
	out := make([]model.Checkin, 0)
	for _, c := range t.s.checkins {
		if c.MemberID != memberID || c.CompletedAt.Before(since) {
			continue
		}
		out = append(out, c)
	}
	// oldest first (insertion order approximates completed_at ASC in tests)
	if limit > 0 && len(out) > int(limit) {
		out = out[:int(limit)]
	}
	return out, nil
}

// ListCohortCheckins mirrors the PostgreSQL query: members whose first check-in is in
// [firstFrom, firstTo), rows before until, ordered by member then time.
func (t *memoryTx) ListCohortCheckins(_ context.Context, firstFrom, firstTo, until time.Time) ([]model.CohortCheckin, error) {
	first := make(map[uuid.UUID]time.Time)
	for _, c := range t.s.checkins {
		if f, ok := first[c.MemberID]; !ok || c.CompletedAt.Before(f) {
			first[c.MemberID] = c.CompletedAt
		}
	}
	out := make([]model.CohortCheckin, 0)
	for _, c := range t.s.checkins {
		f := first[c.MemberID]
		if f.Before(firstFrom) || !f.Before(firstTo) || !c.CompletedAt.Before(until) {
			continue
		}
		out = append(out, model.CohortCheckin{MemberID: c.MemberID, PSS4: c.PSS4, CompletedAt: c.CompletedAt})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MemberID != out[j].MemberID {
			return out[i].MemberID.String() < out[j].MemberID.String()
		}
		return out[i].CompletedAt.Before(out[j].CompletedAt)
	})
	return out, nil
}

// AddCheckin seeds a stored check-in (cohort tests); ID and idempotency key are filled when empty.
func (s *MemoryStore) AddCheckin(c model.Checkin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == uuid.Nil {
		c.ID = uuid.Must(uuid.NewV7())
	}
	if c.IdempotencyKey == uuid.Nil {
		c.IdempotencyKey = uuid.Must(uuid.NewV7())
	}
	s.checkins = append(s.checkins, c)
}

// ListPracticesByLang returns practices for lang.
func (t *memoryTx) ListPracticesByLang(_ context.Context, lang string) ([]model.Practice, error) {
	out := make([]model.Practice, 0)
	for _, p := range t.s.practices {
		if p.Lang == lang {
			out = append(out, p)
		}
	}
	return out, nil
}

// PublishCheckinCompleted records a wellbeing.CheckinCompleted event in memory.
func (t *memoryTx) PublishCheckinCompleted(_ context.Context, now time.Time, data model.CheckinCompletedData) error {
	t.s.events = append(t.s.events, MemoryEvent{
		Type:    model.EventCheckinCompleted,
		Version: model.EventCheckinCompletedVersion,
		Data:    data,
		At:      now,
	})
	return nil
}

// PublishDeliveryRequested records a notify.DeliveryRequested event in memory.
func (t *memoryTx) PublishDeliveryRequested(_ context.Context, now time.Time, data model.DeliveryRequestedData) error {
	t.s.events = append(t.s.events, MemoryEvent{
		Type:    model.EventDeliveryRequested,
		Version: model.EventDeliveryRequestedVersion,
		Data:    data,
		At:      now,
	})
	return nil
}

// ScheduleReminder records a wellbeing.Reminder timer; scheduled is false on dedup hit.
func (t *memoryTx) ScheduleReminder(_ context.Context, memberID uuid.UUID, dueAt time.Time) (bool, error) {
	key := timerKey(model.TimerReminder, memberID)
	if _, ok := t.s.timers[key]; ok {
		return false, nil
	}
	t.s.timers[key] = dueAt
	return true, nil
}

// CancelReminder removes a pending Reminder timer; returns how many were cancelled.
func (t *memoryTx) CancelReminder(_ context.Context, memberID uuid.UUID, _ time.Time) (int64, error) {
	key := timerKey(model.TimerReminder, memberID)
	if _, ok := t.s.timers[key]; !ok {
		return 0, nil
	}
	delete(t.s.timers, key)
	return 1, nil
}

var (
	_ port.Sealer       = (*FakeSealer)(nil)
	_ port.CrisisOpener = (*FakeCrisisOpener)(nil)
	_ port.Store        = (*MemoryStore)(nil)
	_ port.Tx           = (*memoryTx)(nil)
)
