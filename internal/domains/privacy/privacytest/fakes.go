// Package privacytest holds in-memory fakes for privacy unit tests.
package privacytest

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/privacy/model"
	"birge/internal/domains/privacy/port"
)

// FakeShredder records Shred calls.
type FakeShredder struct {
	mu    sync.Mutex
	Calls []uuid.UUID
	Err   error
}

// Shred implements port.KeyShredder.
func (f *FakeShredder) Shred(_ context.Context, memberID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, memberID)
	return f.Err
}

// Len returns how many shred calls were made.
func (f *FakeShredder) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

// MemoryStore is an in-memory port.Store for unit tests.
type MemoryStore struct {
	mu sync.Mutex

	consents  []model.ConsentRecord
	deletions map[uuid.UUID]model.DeletionRequest
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
		deletions: make(map[uuid.UUID]model.DeletionRequest),
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

// ConsentCount returns how many history rows exist for the member and type.
func (s *MemoryStore) ConsentCount(memberID uuid.UUID, consentType string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.consents {
		if c.MemberID == memberID && c.Type == consentType {
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

func timerKey(kind string, subject uuid.UUID) string {
	return kind + "|" + subject.String()
}

type memoryTx struct {
	s *MemoryStore
}

// AppendConsent inserts one append-only consent_history row and returns it with its id.
func (t *memoryTx) AppendConsent(_ context.Context, r model.ConsentRecord) (model.ConsentRecord, error) {
	if r.ID == uuid.Nil {
		r.ID = uuid.Must(uuid.NewV7())
	}
	t.s.consents = append(t.s.consents, r)
	return r, nil
}

// GetLatestConsent returns the newest history row for memberID/type or model.ErrNotFound.
func (t *memoryTx) GetLatestConsent(_ context.Context, memberID uuid.UUID, consentType string) (model.ConsentRecord, error) {
	var (
		found bool
		best  model.ConsentRecord
	)
	for _, c := range t.s.consents {
		if c.MemberID != memberID || c.Type != consentType {
			continue
		}
		if !found || c.ChangedAt.After(best.ChangedAt) || (c.ChangedAt.Equal(best.ChangedAt) && bytesGreater(c.ID, best.ID)) {
			best = c
			found = true
		}
	}
	if !found {
		return model.ConsentRecord{}, model.ErrNotFound
	}
	return best, nil
}

// ListLatestConsents returns the newest history row per consent type for the member.
func (t *memoryTx) ListLatestConsents(_ context.Context, memberID uuid.UUID) ([]model.ConsentRecord, error) {
	byType := make(map[string]model.ConsentRecord)
	for _, c := range t.s.consents {
		if c.MemberID != memberID {
			continue
		}
		prev, ok := byType[c.Type]
		if !ok || c.ChangedAt.After(prev.ChangedAt) || (c.ChangedAt.Equal(prev.ChangedAt) && bytesGreater(c.ID, prev.ID)) {
			byType[c.Type] = c
		}
	}
	out := make([]model.ConsentRecord, 0, len(byType))
	for _, tpe := range model.AllConsentTypes {
		if r, ok := byType[tpe]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// InsertDeletionRequest creates the member's deletion request; inserted is false when one exists.
func (t *memoryTx) InsertDeletionRequest(_ context.Context, r model.DeletionRequest) (model.DeletionRequest, bool, error) {
	if _, ok := t.s.deletions[r.MemberID]; ok {
		return model.DeletionRequest{}, false, nil
	}
	t.s.deletions[r.MemberID] = r
	return r, true, nil
}

// GetDeletionRequest returns the member's deletion request or model.ErrNotFound.
func (t *memoryTx) GetDeletionRequest(_ context.Context, memberID uuid.UUID) (model.DeletionRequest, error) {
	r, ok := t.s.deletions[memberID]
	if !ok {
		return model.DeletionRequest{}, model.ErrNotFound
	}
	return r, nil
}

// MarkDeletionCompleted sets completed_at once; updated is false when absent or already completed.
func (t *memoryTx) MarkDeletionCompleted(_ context.Context, memberID uuid.UUID, at time.Time) (model.DeletionRequest, bool, error) {
	r, ok := t.s.deletions[memberID]
	if !ok {
		return model.DeletionRequest{}, false, nil
	}
	if r.CompletedAt != nil {
		return r, false, nil
	}
	r.CompletedAt = &at
	t.s.deletions[memberID] = r
	return r, true, nil
}

// PublishConsentChanged records a privacy.ConsentChanged event in memory.
func (t *memoryTx) PublishConsentChanged(_ context.Context, now time.Time, data model.ConsentChangedData) error {
	t.s.events = append(t.s.events, MemoryEvent{
		Type:    model.EventConsentChanged,
		Version: model.EventConsentChangedVersion,
		Data:    data,
		At:      now,
	})
	return nil
}

// PublishDataDeletionRequested records a privacy.DataDeletionRequested event in memory.
func (t *memoryTx) PublishDataDeletionRequested(_ context.Context, now time.Time, data model.DataDeletionRequestedData) error {
	t.s.events = append(t.s.events, MemoryEvent{
		Type:    model.EventDataDeletionRequested,
		Version: model.EventDataDeletionVersion,
		Data:    data,
		At:      now,
	})
	return nil
}

// ScheduleDeletionDue records a privacy.DeletionDue timer; scheduled is false on dedup hit.
func (t *memoryTx) ScheduleDeletionDue(_ context.Context, memberID uuid.UUID, dueAt time.Time) (bool, error) {
	key := timerKey(model.TimerDeletionDue, memberID)
	if _, ok := t.s.timers[key]; ok {
		return false, nil
	}
	t.s.timers[key] = dueAt
	return true, nil
}

func bytesGreater(a, b uuid.UUID) bool {
	ab, bb := a[:], b[:]
	for i := range ab {
		if ab[i] > bb[i] {
			return true
		}
		if ab[i] < bb[i] {
			return false
		}
	}
	return false
}

var (
	_ port.KeyShredder = (*FakeShredder)(nil)
	_ port.Store       = (*MemoryStore)(nil)
	_ port.Tx          = (*memoryTx)(nil)
)
