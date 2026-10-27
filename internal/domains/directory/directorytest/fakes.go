// Package directorytest holds in-memory fakes of the directory ports for unit tests.
package directorytest

import (
	"bytes"
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/directory/model"
	"birge/internal/domains/directory/port"
)

// Clock is a fixed clock for tests.
type Clock struct{ T time.Time }

// Now implements clock.Clock.
func (c *Clock) Now() time.Time { return c.T }

// FakeMembers implements MemberInfo.
type FakeMembers struct {
	mu       sync.Mutex
	profiles map[uuid.UUID]model.Profile
	Fail     error
}

// NewFakeMembers returns an empty FakeMembers.
func NewFakeMembers() *FakeMembers {
	return &FakeMembers{profiles: map[uuid.UUID]model.Profile{}}
}

// Put stores a profile.
func (f *FakeMembers) Put(id uuid.UUID, p model.Profile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles[id] = p
}

// Profiles implements port.MemberInfo.
func (f *FakeMembers) Profiles(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]model.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return nil, f.Fail
	}
	out := map[uuid.UUID]model.Profile{}
	for _, id := range ids {
		if p, ok := f.profiles[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

type state struct {
	specialists map[uuid.UUID]model.Specialist
	reviews     map[uuid.UUID]model.Review
	events      []model.Event
	processed   map[string]map[uuid.UUID]bool
}

func (s *state) clone() *state {
	proc := map[string]map[uuid.UUID]bool{}
	for k, v := range s.processed {
		proc[k] = maps.Clone(v)
	}
	return &state{
		specialists: maps.Clone(s.specialists), reviews: maps.Clone(s.reviews),
		events: slices.Clone(s.events), processed: proc,
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
		specialists: map[uuid.UUID]model.Specialist{}, reviews: map[uuid.UUID]model.Review{},
		processed: map[string]map[uuid.UUID]bool{},
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

// EventsOfType returns committed events of type t.
func (m *MemoryStore) EventsOfType(t string) []model.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Event
	for _, e := range m.st.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// Specialist returns a specialist by id.
func (m *MemoryStore) Specialist(id uuid.UUID) (model.Specialist, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.st.specialists[id]
	return s, ok
}

// Review returns a review by id.
func (m *MemoryStore) Review(id uuid.UUID) (model.Review, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.st.reviews[id]
	return r, ok
}

type memoryTx struct {
	st     *state
	failOn func(method string) error
}

func (t *memoryTx) check(method string) error {
	if t.failOn != nil {
		return t.failOn(method)
	}
	return nil
}

// InsertSpecialist implements port.Tx.
func (t *memoryTx) InsertSpecialist(_ context.Context, s model.Specialist) (model.Specialist, bool, error) {
	if err := t.check("InsertSpecialist"); err != nil {
		return model.Specialist{}, false, err
	}
	for _, e := range t.st.specialists {
		if e.AddedBy == s.AddedBy && e.IdempotencyKey == s.IdempotencyKey {
			return model.Specialist{}, false, nil
		}
	}
	t.st.specialists[s.ID] = s
	return s, true, nil
}

// GetSpecialistByIdempotencyKey implements port.Tx.
func (t *memoryTx) GetSpecialistByIdempotencyKey(_ context.Context, addedBy, key uuid.UUID) (model.Specialist, error) {
	for _, s := range t.st.specialists {
		if s.AddedBy == addedBy && s.IdempotencyKey == key {
			return s, nil
		}
	}
	return model.Specialist{}, model.ErrNotFound
}

// GetSpecialist implements port.Tx.
func (t *memoryTx) GetSpecialist(_ context.Context, id uuid.UUID, _ bool) (model.Specialist, error) {
	s, ok := t.st.specialists[id]
	if !ok {
		return model.Specialist{}, model.ErrNotFound
	}
	return s, nil
}

// SearchActiveSpecialists implements port.Tx.
func (t *memoryTx) SearchActiveSpecialists(_ context.Context, f model.SearchFilter) ([]model.Specialist, error) {
	var out []model.Specialist
	for _, s := range t.st.specialists {
		if s.Status != model.StatusActive {
			continue
		}
		if f.Profession != nil && s.Profession != *f.Profession {
			continue
		}
		if f.DistrictID != nil && s.DistrictID != *f.DistrictID {
			continue
		}
		if f.Need != nil {
			found := false
			for _, n := range s.Needs {
				if n == *f.Need {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if f.CursorCreatedAt != nil && f.CursorID != nil {
			afterCursor := s.CreatedAt.After(*f.CursorCreatedAt) ||
				(s.CreatedAt.Equal(*f.CursorCreatedAt) && bytes.Compare(s.ID[:], f.CursorID[:]) >= 0)
			if afterCursor {
				continue
			}
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b model.Specialist) int {
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return 1
		}
		return bytes.Compare(b.ID[:], a.ID[:])
	})
	if int(f.Limit) < len(out) {
		out = out[:f.Limit]
	}
	return out, nil
}

// ApproveSpecialist implements port.Tx.
func (t *memoryTx) ApproveSpecialist(_ context.Context, id uuid.UUID) (model.Specialist, bool, error) {
	s, ok := t.st.specialists[id]
	if !ok || s.Status != model.StatusPending {
		return model.Specialist{}, false, nil
	}
	s.Status = model.StatusActive
	s.RejectReason = nil
	t.st.specialists[id] = s
	return s, true, nil
}

// RejectSpecialist implements port.Tx.
func (t *memoryTx) RejectSpecialist(_ context.Context, id uuid.UUID, reason string) (model.Specialist, bool, error) {
	s, ok := t.st.specialists[id]
	if !ok || s.Status != model.StatusPending {
		return model.Specialist{}, false, nil
	}
	s.Status = model.StatusRejected
	s.RejectReason = &reason
	t.st.specialists[id] = s
	return s, true, nil
}

// UpsertReview implements port.Tx.
func (t *memoryTx) UpsertReview(_ context.Context, r model.Review) (model.Review, error) {
	if err := t.check("UpsertReview"); err != nil {
		return model.Review{}, err
	}
	for id, e := range t.st.reviews {
		if e.SpecialistID == r.SpecialistID && e.AuthorID == r.AuthorID {
			// Keep the existing id (matches SQL ON CONFLICT).
			e.Rating = r.Rating
			e.Text = r.Text
			e.Status = model.StatusPending
			e.RejectReason = nil
			e.UpdatedAt = r.UpdatedAt
			t.st.reviews[id] = e
			return e, nil
		}
	}
	t.st.reviews[r.ID] = r
	return r, nil
}

// GetReview implements port.Tx.
func (t *memoryTx) GetReview(_ context.Context, id uuid.UUID, _ bool) (model.Review, error) {
	r, ok := t.st.reviews[id]
	if !ok {
		return model.Review{}, model.ErrNotFound
	}
	return r, nil
}

// ListLatestActiveReviews implements port.Tx.
func (t *memoryTx) ListLatestActiveReviews(_ context.Context, specialistID uuid.UUID, limit int32) ([]model.Review, error) {
	var out []model.Review
	for _, r := range t.st.reviews {
		if r.SpecialistID == specialistID && r.Status == model.StatusActive {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b model.Review) int {
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return 1
		}
		return bytes.Compare(b.ID[:], a.ID[:])
	})
	if int(limit) < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// AverageActiveRating implements port.Tx.
func (t *memoryTx) AverageActiveRating(_ context.Context, specialistID uuid.UUID) (model.RatingStats, error) {
	var sum float64
	var n int
	for _, r := range t.st.reviews {
		if r.SpecialistID == specialistID && r.Status == model.StatusActive {
			sum += float64(r.Rating)
			n++
		}
	}
	if n == 0 {
		return model.RatingStats{}, nil
	}
	return model.RatingStats{Average: sum / float64(n), Count: n}, nil
}

// ApproveReview implements port.Tx.
func (t *memoryTx) ApproveReview(_ context.Context, id uuid.UUID, at time.Time) (model.Review, bool, error) {
	r, ok := t.st.reviews[id]
	if !ok || r.Status != model.StatusPending {
		return model.Review{}, false, nil
	}
	r.Status = model.StatusActive
	r.RejectReason = nil
	r.UpdatedAt = at
	t.st.reviews[id] = r
	return r, true, nil
}

// RejectReview implements port.Tx.
func (t *memoryTx) RejectReview(_ context.Context, id uuid.UUID, reason string, at time.Time) (model.Review, bool, error) {
	r, ok := t.st.reviews[id]
	if !ok || r.Status != model.StatusPending {
		return model.Review{}, false, nil
	}
	r.Status = model.StatusRejected
	r.RejectReason = &reason
	r.UpdatedAt = at
	t.st.reviews[id] = r
	return r, true, nil
}

// Publish implements port.Tx.
func (t *memoryTx) Publish(_ context.Context, _ time.Time, e model.Event) error {
	if err := t.check("Publish:" + e.Type); err != nil {
		return err
	}
	t.st.events = append(t.st.events, e)
	return nil
}

// MarkProcessed implements port.Tx.
func (t *memoryTx) MarkProcessed(_ context.Context, consumer string, eventID uuid.UUID) (bool, error) {
	if t.st.processed[consumer] == nil {
		t.st.processed[consumer] = map[uuid.UUID]bool{}
	}
	if t.st.processed[consumer][eventID] {
		return false, nil
	}
	t.st.processed[consumer][eventID] = true
	return true, nil
}

var _ port.Store = (*MemoryStore)(nil)
var _ port.Tx = (*memoryTx)(nil)
var _ port.MemberInfo = (*FakeMembers)(nil)
