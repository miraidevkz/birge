// Package safetytest holds in-memory fakes of the safety ports for unit
// tests. The fakes are for tests only: FakeSealer does not encrypt.
package safetytest

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/safety/model"
	"birge/internal/domains/safety/port"
)

// Timer is a recorded backup timer.
type Timer struct {
	DueAt     time.Time
	Cancelled bool
}

// state is everything MemoryStore keeps; a transaction works on a copy.
type state struct {
	escalations map[uuid.UUID]model.Escalation
	shifts      map[uuid.UUID]model.DutyShift
	events      []model.Event
	timers      map[uuid.UUID]Timer
	audit       []model.AuditEntry
}

func (s *state) clone() *state {
	return &state{
		escalations: maps.Clone(s.escalations),
		shifts:      maps.Clone(s.shifts),
		events:      slices.Clone(s.events),
		timers:      maps.Clone(s.timers),
		audit:       slices.Clone(s.audit),
	}
}

// MemoryStore is an in-memory port.Store. Transactions are serialised and
// atomic: a failing transaction leaves no trace.
type MemoryStore struct {
	mu sync.Mutex
	st *state
	// FailOn, when set, is called before each Tx method with its name; a
	// non-nil result is returned by that method (to test rollbacks).
	FailOn func(method string) error
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{st: &state{
		escalations: map[uuid.UUID]model.Escalation{},
		shifts:      map[uuid.UUID]model.DutyShift{},
		timers:      map[uuid.UUID]Timer{},
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

// Tx returns a port.Tx bound to a new transaction and a commit function, for
// calling handlers that take a Tx (like the timer handler).
func (m *MemoryStore) Tx() (port.Tx, func()) {
	m.mu.Lock()
	work := m.st.clone()
	return &memoryTx{st: work, failOn: m.FailOn}, func() {
		m.st = work
		m.mu.Unlock()
	}
}

// Events returns the committed outbox events.
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

// Timer returns the backup timer of the escalation.
func (m *MemoryStore) Timer(escalationID uuid.UUID) (Timer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.st.timers[escalationID]
	return t, ok
}

// Audit returns the committed audit entries.
func (m *MemoryStore) Audit() []model.AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.st.audit)
}

// Escalation returns a committed escalation.
func (m *MemoryStore) Escalation(id uuid.UUID) (model.Escalation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.st.escalations[id]
	return e, ok
}

// Escalations returns how many escalations are stored.
func (m *MemoryStore) Escalations() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.st.escalations)
}

// PutShift stores a shift directly (test setup); Phone must already be sealed.
func (m *MemoryStore) PutShift(s model.DutyShift) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.shifts[s.ID] = s
}

type memoryTx struct {
	st     *state
	failOn func(string) error
}

func (t *memoryTx) fail(method string) error {
	if t.failOn == nil {
		return nil
	}
	return t.failOn(method)
}

func active(e model.Escalation) bool {
	return e.Status == model.StatusOpen || e.Status == model.StatusTaken
}

// InsertEscalation implements port.Tx.
func (t *memoryTx) InsertEscalation(_ context.Context, e model.Escalation) (model.Escalation, bool, error) {
	if err := t.fail("InsertEscalation"); err != nil {
		return model.Escalation{}, false, err
	}
	for _, cur := range t.st.escalations {
		if cur.MemberID == e.MemberID && active(cur) {
			return model.Escalation{}, false, nil
		}
	}
	e.ID = uuid.Must(uuid.NewV7())
	t.st.escalations[e.ID] = e
	return e, true, nil
}

// GetActiveEscalation implements port.Tx.
func (t *memoryTx) GetActiveEscalation(_ context.Context, memberID uuid.UUID) (model.Escalation, error) {
	if err := t.fail("GetActiveEscalation"); err != nil {
		return model.Escalation{}, err
	}
	for _, cur := range t.st.escalations {
		if cur.MemberID == memberID && active(cur) {
			return cur, nil
		}
	}
	return model.Escalation{}, model.ErrNotFound
}

// GetEscalation implements port.Tx.
func (t *memoryTx) GetEscalation(_ context.Context, id uuid.UUID, _ bool) (model.Escalation, error) {
	if err := t.fail("GetEscalation"); err != nil {
		return model.Escalation{}, err
	}
	e, ok := t.st.escalations[id]
	if !ok {
		return model.Escalation{}, model.ErrNotFound
	}
	return e, nil
}

// TakeEscalation implements port.Tx.
func (t *memoryTx) TakeEscalation(_ context.Context, id, staffID uuid.UUID, at time.Time) (model.Escalation, bool, error) {
	if err := t.fail("TakeEscalation"); err != nil {
		return model.Escalation{}, false, err
	}
	e, ok := t.st.escalations[id]
	if !ok || e.Status != model.StatusOpen {
		return model.Escalation{}, false, nil
	}
	e.Status, e.TakenBy, e.TakenAt = model.StatusTaken, &staffID, &at
	t.st.escalations[id] = e
	return e, true, nil
}

// CloseEscalation implements port.Tx.
func (t *memoryTx) CloseEscalation(_ context.Context, id, staffID uuid.UUID, outcome string, note []byte, at time.Time) (model.Escalation, bool, error) {
	if err := t.fail("CloseEscalation"); err != nil {
		return model.Escalation{}, false, err
	}
	e, ok := t.st.escalations[id]
	if !ok || e.Status != model.StatusTaken || !e.TakenByStaff(staffID) {
		return model.Escalation{}, false, nil
	}
	e.Status, e.Outcome, e.Note, e.ClosedAt = model.StatusClosed, &outcome, bytes.Clone(note), &at
	t.st.escalations[id] = e
	return e, true, nil
}

// ListEscalations implements port.Tx.
func (t *memoryTx) ListEscalations(_ context.Context, f model.EscalationFilter) ([]model.Escalation, error) {
	if err := t.fail("ListEscalations"); err != nil {
		return nil, err
	}
	var out []model.Escalation
	for _, e := range t.st.escalations {
		if f.Status != "" && e.Status != f.Status {
			continue
		}
		if f.CursorOpenedAt != nil && !before(e, *f.CursorOpenedAt, *f.CursorID) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return before(out[j], out[i].OpenedAt, out[i].ID) })
	if len(out) > int(f.Limit) {
		out = out[:f.Limit]
	}
	return out, nil
}

// before reports whether (e.OpenedAt, e.ID) < (at, id).
func before(e model.Escalation, at time.Time, id uuid.UUID) bool {
	if !e.OpenedAt.Equal(at) {
		return e.OpenedAt.Before(at)
	}
	return bytes.Compare(e.ID[:], id[:]) < 0
}

// InsertDutyShift implements port.Tx.
func (t *memoryTx) InsertDutyShift(_ context.Context, s model.DutyShift) (model.DutyShift, bool, error) {
	if err := t.fail("InsertDutyShift"); err != nil {
		return model.DutyShift{}, false, err
	}
	if _, ok := t.st.shifts[s.ID]; ok {
		return model.DutyShift{}, false, nil
	}
	t.st.shifts[s.ID] = s
	return s, true, nil
}

// GetDutyShift implements port.Tx.
func (t *memoryTx) GetDutyShift(_ context.Context, id uuid.UUID) (model.DutyShift, error) {
	if err := t.fail("GetDutyShift"); err != nil {
		return model.DutyShift{}, err
	}
	s, ok := t.st.shifts[id]
	if !ok {
		return model.DutyShift{}, model.ErrNotFound
	}
	return s, nil
}

// ListDutyShifts implements port.Tx.
func (t *memoryTx) ListDutyShifts(_ context.Context, from, to time.Time, limit int32) ([]model.DutyShift, error) {
	if err := t.fail("ListDutyShifts"); err != nil {
		return nil, err
	}
	var out []model.DutyShift
	for _, s := range t.st.shifts {
		if s.CancelledAt == nil && s.StartsAt.Before(to) && s.EndsAt.After(from) {
			out = append(out, s)
		}
	}
	sortShifts(out)
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

// ListCoveringShifts implements port.Tx.
func (t *memoryTx) ListCoveringShifts(_ context.Context, role string, backup bool, at time.Time) ([]model.DutyShift, error) {
	if err := t.fail("ListCoveringShifts"); err != nil {
		return nil, err
	}
	var out []model.DutyShift
	for _, s := range t.st.shifts {
		if s.CancelledAt == nil && s.Role == role && s.Backup == backup && !s.StartsAt.After(at) && s.EndsAt.After(at) {
			out = append(out, s)
		}
	}
	sortShifts(out)
	return out, nil
}

func sortShifts(s []model.DutyShift) {
	sort.Slice(s, func(i, j int) bool {
		if !s[i].StartsAt.Equal(s[j].StartsAt) {
			return s[i].StartsAt.Before(s[j].StartsAt)
		}
		return bytes.Compare(s[i].ID[:], s[j].ID[:]) < 0
	})
}

// CancelDutyShift implements port.Tx.
func (t *memoryTx) CancelDutyShift(_ context.Context, id uuid.UUID, at time.Time) (model.DutyShift, bool, error) {
	if err := t.fail("CancelDutyShift"); err != nil {
		return model.DutyShift{}, false, err
	}
	s, ok := t.st.shifts[id]
	if !ok || s.CancelledAt != nil || !s.StartsAt.After(at) {
		return model.DutyShift{}, false, nil
	}
	s.CancelledAt = &at
	t.st.shifts[id] = s
	return s, true, nil
}

// Publish implements port.Tx.
func (t *memoryTx) Publish(_ context.Context, _ time.Time, e model.Event) error {
	if err := t.fail("Publish"); err != nil {
		return err
	}
	t.st.events = append(t.st.events, e)
	return nil
}

// ScheduleBackup implements port.Tx.
func (t *memoryTx) ScheduleBackup(_ context.Context, id uuid.UUID, dueAt time.Time) (bool, error) {
	if err := t.fail("ScheduleBackup"); err != nil {
		return false, err
	}
	if cur, ok := t.st.timers[id]; ok && !cur.Cancelled {
		return false, nil
	}
	t.st.timers[id] = Timer{DueAt: dueAt}
	return true, nil
}

// CancelBackup implements port.Tx.
func (t *memoryTx) CancelBackup(_ context.Context, id uuid.UUID, _ time.Time) (int64, error) {
	if err := t.fail("CancelBackup"); err != nil {
		return 0, err
	}
	cur, ok := t.st.timers[id]
	if !ok || cur.Cancelled {
		return 0, nil
	}
	cur.Cancelled = true
	t.st.timers[id] = cur
	return 1, nil
}

// Audit implements port.Tx.
func (t *memoryTx) Audit(_ context.Context, e model.AuditEntry) error {
	if err := t.fail("Audit"); err != nil {
		return err
	}
	t.st.audit = append(t.st.audit, e)
	return nil
}

// FakeSealer "seals" by prefixing the subject and associated data, so tests
// can check that a ciphertext opens only for its own subject and row. It
// does not encrypt.
type FakeSealer struct {
	// Err, when set, is returned by Seal and Open.
	Err error
}

var errFakeOpen = errors.New("safetytest: ciphertext does not match subject or associated data")

func fakeHeader(subjectID uuid.UUID, ad []byte) []byte {
	return append(append([]byte("sealed|"+subjectID.String()+"|"), ad...), '|')
}

// Seal implements port.Sealer.
func (f *FakeSealer) Seal(_ context.Context, subjectID uuid.UUID, plaintext, ad []byte) ([]byte, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return append(fakeHeader(subjectID, ad), plaintext...), nil
}

// Open implements port.Sealer.
func (f *FakeSealer) Open(_ context.Context, subjectID uuid.UUID, ciphertext, ad []byte) ([]byte, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	h := fakeHeader(subjectID, ad)
	if !bytes.HasPrefix(ciphertext, h) {
		return nil, errFakeOpen
	}
	return bytes.Clone(ciphertext[len(h):]), nil
}

// FakeDirectory is an in-memory port.MemberDirectory.
type FakeDirectory struct {
	mu        sync.Mutex
	Contacts  map[uuid.UUID]model.Contact
	Districts map[uuid.UUID]uuid.UUID
	// Err, when set, is returned by every call.
	Err error
	// ContactCalls counts Contact calls.
	ContactCalls int
}

// Contact implements port.MemberDirectory.
func (f *FakeDirectory) Contact(_ context.Context, memberID uuid.UUID) (model.Contact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ContactCalls++
	if f.Err != nil {
		return model.Contact{}, f.Err
	}
	c, ok := f.Contacts[memberID]
	if !ok {
		return model.Contact{}, model.ErrNotFound
	}
	return c, nil
}

// District implements port.MemberDirectory.
func (f *FakeDirectory) District(_ context.Context, memberID uuid.UUID) (*uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	d, ok := f.Districts[memberID]
	if !ok {
		return nil, nil
	}
	return &d, nil
}

// Clock is a settable clock.
type Clock struct {
	mu sync.Mutex
	T  time.Time
}

// Now implements clock.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.T
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.T = c.T.Add(d)
}
