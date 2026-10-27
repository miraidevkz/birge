// Package sharingtest holds in-memory fakes of the sharing ports for unit tests.
package sharingtest

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
)

// Clock is a fixed clock for tests.
type Clock struct{ T time.Time }

// Now implements clock.Clock.
func (c *Clock) Now() time.Time { return c.T }

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) { c.T = c.T.Add(d) }

// FakeMembers implements MemberInfo and Blocks.
type FakeMembers struct {
	mu       sync.Mutex
	profiles map[uuid.UUID]model.Profile
	blocks   map[[2]uuid.UUID]bool
	Fail     error
}

// NewFakeMembers returns an empty FakeMembers.
func NewFakeMembers() *FakeMembers {
	return &FakeMembers{profiles: map[uuid.UUID]model.Profile{}, blocks: map[[2]uuid.UUID]bool{}}
}

// Put stores a profile.
func (f *FakeMembers) Put(id uuid.UUID, p model.Profile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles[id] = p
}

// Block records a mutual invisibility between a and b.
func (f *FakeMembers) Block(a, b uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocks[canon(a, b)] = true
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

// IsBlocked implements port.Blocks.
func (f *FakeMembers) IsBlocked(_ context.Context, a, b uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return false, f.Fail
	}
	return f.blocks[canon(a, b)], nil
}

func canon(a, b uuid.UUID) [2]uuid.UUID {
	if bytes.Compare(a[:], b[:]) <= 0 {
		return [2]uuid.UUID{a, b}
	}
	return [2]uuid.UUID{b, a}
}

// MemoryObjects is an in-memory ObjectStore.
type MemoryObjects struct {
	mu   sync.Mutex
	data map[string][]byte
	Fail error
}

// NewMemoryObjects returns an empty object store.
func NewMemoryObjects() *MemoryObjects {
	return &MemoryObjects{data: map[string][]byte{}}
}

// PresignPut implements port.ObjectStore.
func (o *MemoryObjects) PresignPut(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	if o.Fail != nil {
		return "", o.Fail
	}
	return "https://objects.example.invalid/put/" + key, nil
}

// PresignGet implements port.ObjectStore.
func (o *MemoryObjects) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	if o.Fail != nil {
		return "", o.Fail
	}
	return "https://objects.example.invalid/get/" + key, nil
}

// Get implements port.ObjectStore.
func (o *MemoryObjects) Get(_ context.Context, key string) ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.Fail != nil {
		return nil, o.Fail
	}
	b, ok := o.data[key]
	if !ok {
		return nil, fmt.Errorf("missing object %s", key)
	}
	return slices.Clone(b), nil
}

// Put implements port.ObjectStore.
func (o *MemoryObjects) Put(_ context.Context, key string, body []byte, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.Fail != nil {
		return o.Fail
	}
	o.data[key] = slices.Clone(body)
	return nil
}

// Delete implements port.ObjectStore.
func (o *MemoryObjects) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.data, key)
	return nil
}

// Has reports whether key exists.
func (o *MemoryObjects) Has(key string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.data[key]
	return ok
}

// GetBytes returns stored bytes for assertions.
func (o *MemoryObjects) GetBytes(key string) []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.data[key])
}

// PassthroughImages returns the raw bytes unchanged (for non-EXIF unit tests).
type PassthroughImages struct{}

// Process implements port.ImageProcessor.
func (PassthroughImages) Process(_ context.Context, raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, model.ErrPhoto
	}
	return slices.Clone(raw), nil
}

// Timer is a recorded platform timer.
type Timer struct {
	Kind      string
	SubjectID uuid.UUID
	DueAt     time.Time
	Cancelled bool
}

type state struct {
	listings  map[uuid.UUID]model.Listing
	photos    map[uuid.UUID]model.PhotoUpload
	contacts  map[uuid.UUID]model.ContactRequest
	events    []model.Event
	timers    []Timer
	processed map[string]map[uuid.UUID]bool
}

func (s *state) clone() *state {
	proc := map[string]map[uuid.UUID]bool{}
	for k, v := range s.processed {
		proc[k] = maps.Clone(v)
	}
	return &state{
		listings: maps.Clone(s.listings), photos: maps.Clone(s.photos),
		contacts: maps.Clone(s.contacts), events: slices.Clone(s.events),
		timers: slices.Clone(s.timers), processed: proc,
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
		listings: map[uuid.UUID]model.Listing{}, photos: map[uuid.UUID]model.PhotoUpload{},
		contacts: map[uuid.UUID]model.ContactRequest{}, processed: map[string]map[uuid.UUID]bool{},
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

// Timers returns committed timers.
func (m *MemoryStore) Timers() []Timer {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.st.timers)
}

// Listing returns a listing by id.
func (m *MemoryStore) Listing(id uuid.UUID) (model.Listing, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.st.listings[id]
	return l, ok
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

// InsertListing implements port.Tx.
func (t *memoryTx) InsertListing(_ context.Context, l model.Listing) (model.Listing, bool, error) {
	if err := t.check("InsertListing"); err != nil {
		return model.Listing{}, false, err
	}
	for _, e := range t.st.listings {
		if e.AuthorID == l.AuthorID && e.IdempotencyKey == l.IdempotencyKey {
			return model.Listing{}, false, nil
		}
	}
	t.st.listings[l.ID] = l
	return l, true, nil
}

// GetListingByIdempotencyKey implements port.Tx.
func (t *memoryTx) GetListingByIdempotencyKey(_ context.Context, authorID, key uuid.UUID) (model.Listing, error) {
	if err := t.check("GetListingByIdempotencyKey"); err != nil {
		return model.Listing{}, err
	}
	for _, l := range t.st.listings {
		if l.AuthorID == authorID && l.IdempotencyKey == key {
			return l, nil
		}
	}
	return model.Listing{}, model.ErrNotFound
}

// GetListing implements port.Tx.
func (t *memoryTx) GetListing(_ context.Context, id uuid.UUID, _ bool) (model.Listing, error) {
	if err := t.check("GetListing"); err != nil {
		return model.Listing{}, err
	}
	l, ok := t.st.listings[id]
	if !ok {
		return model.Listing{}, model.ErrNotFound
	}
	return l, nil
}

// ListMyListings implements port.Tx.
func (t *memoryTx) ListMyListings(_ context.Context, authorID uuid.UUID, limit int32) ([]model.Listing, error) {
	if err := t.check("ListMyListings"); err != nil {
		return nil, err
	}
	var out []model.Listing
	for _, l := range t.st.listings {
		if l.AuthorID == authorID {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b model.Listing) int {
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

// ListActiveListings implements port.Tx.
func (t *memoryTx) ListActiveListings(_ context.Context, f model.FeedFilter) ([]model.Listing, error) {
	if err := t.check("ListActiveListings"); err != nil {
		return nil, err
	}
	var out []model.Listing
	for _, l := range t.st.listings {
		if l.Status != model.ListingActive || l.AuthorID == f.ViewerID {
			continue
		}
		if f.Type != nil && l.Type != *f.Type {
			continue
		}
		if f.Category != nil && l.Category != *f.Category {
			continue
		}
		if f.DistrictID != nil && l.DistrictID != *f.DistrictID {
			continue
		}
		if f.CursorCreatedAt != nil && f.CursorID != nil {
			afterCursor := l.CreatedAt.After(*f.CursorCreatedAt) ||
				(l.CreatedAt.Equal(*f.CursorCreatedAt) && bytes.Compare(l.ID[:], f.CursorID[:]) >= 0)
			if afterCursor {
				continue
			}
		}
		out = append(out, l)
	}
	slices.SortFunc(out, func(a, b model.Listing) int {
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

// SetListingPhotoObjectKey implements port.Tx.
func (t *memoryTx) SetListingPhotoObjectKey(_ context.Context, id uuid.UUID, key string) (model.Listing, error) {
	if err := t.check("SetListingPhotoObjectKey"); err != nil {
		return model.Listing{}, err
	}
	l, ok := t.st.listings[id]
	if !ok {
		return model.Listing{}, model.ErrNotFound
	}
	l.PhotoObjectKey = &key
	t.st.listings[id] = l
	return l, nil
}

// ApproveListing implements port.Tx.
func (t *memoryTx) ApproveListing(_ context.Context, id uuid.UUID, at time.Time) (model.Listing, bool, error) {
	if err := t.check("ApproveListing"); err != nil {
		return model.Listing{}, false, err
	}
	l, ok := t.st.listings[id]
	if !ok || l.Status != model.ListingPending {
		return model.Listing{}, false, nil
	}
	l.ApprovedAt = &at
	t.st.listings[id] = l
	return l, true, nil
}

// ActivateListing implements port.Tx.
func (t *memoryTx) ActivateListing(_ context.Context, id uuid.UUID, closesAt time.Time) (model.Listing, bool, error) {
	if err := t.check("ActivateListing"); err != nil {
		return model.Listing{}, false, err
	}
	l, ok := t.st.listings[id]
	if !ok || l.Status != model.ListingPending || l.ApprovedAt == nil || !l.PhotoReady() {
		return model.Listing{}, false, nil
	}
	l.Status = model.ListingActive
	l.ClosesAt = &closesAt
	t.st.listings[id] = l
	return l, true, nil
}

// RejectListing implements port.Tx.
func (t *memoryTx) RejectListing(_ context.Context, id uuid.UUID, reason string) (model.Listing, bool, error) {
	if err := t.check("RejectListing"); err != nil {
		return model.Listing{}, false, err
	}
	l, ok := t.st.listings[id]
	if !ok || l.Status != model.ListingPending {
		return model.Listing{}, false, nil
	}
	l.Status = model.ListingRejected
	l.RejectReason = &reason
	l.ApprovedAt = nil
	l.ClosesAt = nil
	t.st.listings[id] = l
	return l, true, nil
}

// CloseListing implements port.Tx.
func (t *memoryTx) CloseListing(_ context.Context, id uuid.UUID, at time.Time) (model.Listing, bool, error) {
	if err := t.check("CloseListing"); err != nil {
		return model.Listing{}, false, err
	}
	l, ok := t.st.listings[id]
	if !ok || l.Status != model.ListingActive {
		return model.Listing{}, false, nil
	}
	l.Status = model.ListingClosed
	l.ClosedAt = &at
	t.st.listings[id] = l
	return l, true, nil
}

// InsertPhotoUpload implements port.Tx.
func (t *memoryTx) InsertPhotoUpload(_ context.Context, p model.PhotoUpload) (model.PhotoUpload, error) {
	if err := t.check("InsertPhotoUpload"); err != nil {
		return model.PhotoUpload{}, err
	}
	t.st.photos[p.ID] = p
	return p, nil
}

// GetPhotoUpload implements port.Tx.
func (t *memoryTx) GetPhotoUpload(_ context.Context, id uuid.UUID, _ bool) (model.PhotoUpload, error) {
	if err := t.check("GetPhotoUpload"); err != nil {
		return model.PhotoUpload{}, err
	}
	p, ok := t.st.photos[id]
	if !ok {
		return model.PhotoUpload{}, model.ErrNotFound
	}
	return p, nil
}

// MarkPhotoUploadUsed implements port.Tx.
func (t *memoryTx) MarkPhotoUploadUsed(_ context.Context, id, memberID uuid.UUID, usedAt, now time.Time) (model.PhotoUpload, bool, error) {
	if err := t.check("MarkPhotoUploadUsed"); err != nil {
		return model.PhotoUpload{}, false, err
	}
	p, ok := t.st.photos[id]
	if !ok || p.MemberID != memberID || p.UsedAt != nil || !p.ExpiresAt.After(now) || p.Status != model.PhotoPending {
		return model.PhotoUpload{}, false, nil
	}
	p.UsedAt = &usedAt
	t.st.photos[id] = p
	return p, true, nil
}

// MarkPhotoUploadProcessed implements port.Tx.
func (t *memoryTx) MarkPhotoUploadProcessed(_ context.Context, id uuid.UUID, processedKey string) (model.PhotoUpload, bool, error) {
	if err := t.check("MarkPhotoUploadProcessed"); err != nil {
		return model.PhotoUpload{}, false, err
	}
	p, ok := t.st.photos[id]
	if !ok || p.Status != model.PhotoPending {
		return model.PhotoUpload{}, false, nil
	}
	p.Status = model.PhotoProcessed
	p.ProcessedObjectKey = &processedKey
	t.st.photos[id] = p
	return p, true, nil
}

// MarkPhotoUploadFailed implements port.Tx.
func (t *memoryTx) MarkPhotoUploadFailed(_ context.Context, id uuid.UUID) (model.PhotoUpload, bool, error) {
	if err := t.check("MarkPhotoUploadFailed"); err != nil {
		return model.PhotoUpload{}, false, err
	}
	p, ok := t.st.photos[id]
	if !ok || p.Status != model.PhotoPending {
		return model.PhotoUpload{}, false, nil
	}
	p.Status = model.PhotoFailed
	t.st.photos[id] = p
	return p, true, nil
}

// InsertContactRequest implements port.Tx.
func (t *memoryTx) InsertContactRequest(_ context.Context, c model.ContactRequest) (model.ContactRequest, bool, error) {
	if err := t.check("InsertContactRequest"); err != nil {
		return model.ContactRequest{}, false, err
	}
	for _, e := range t.st.contacts {
		if e.ListingID == c.ListingID && e.RequesterID == c.RequesterID {
			return model.ContactRequest{}, false, nil
		}
	}
	t.st.contacts[c.ID] = c
	return c, true, nil
}

// GetContactRequestByListingRequester implements port.Tx.
func (t *memoryTx) GetContactRequestByListingRequester(_ context.Context, listingID, requesterID uuid.UUID) (model.ContactRequest, error) {
	if err := t.check("GetContactRequestByListingRequester"); err != nil {
		return model.ContactRequest{}, err
	}
	for _, c := range t.st.contacts {
		if c.ListingID == listingID && c.RequesterID == requesterID {
			return c, nil
		}
	}
	return model.ContactRequest{}, model.ErrNotFound
}

// GetContactRequest implements port.Tx.
func (t *memoryTx) GetContactRequest(_ context.Context, id uuid.UUID, _ bool) (model.ContactRequest, error) {
	if err := t.check("GetContactRequest"); err != nil {
		return model.ContactRequest{}, err
	}
	c, ok := t.st.contacts[id]
	if !ok {
		return model.ContactRequest{}, model.ErrNotFound
	}
	return c, nil
}

// AcceptContactRequest implements port.Tx.
func (t *memoryTx) AcceptContactRequest(_ context.Context, id uuid.UUID, at time.Time) (model.ContactRequest, bool, error) {
	if err := t.check("AcceptContactRequest"); err != nil {
		return model.ContactRequest{}, false, err
	}
	c, ok := t.st.contacts[id]
	if !ok || c.Status != model.ContactPending {
		return model.ContactRequest{}, false, nil
	}
	c.Status = model.ContactAccepted
	c.AcceptedAt = &at
	t.st.contacts[id] = c
	return c, true, nil
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
	if err := t.check("MarkProcessed"); err != nil {
		return false, err
	}
	if t.st.processed[consumer] == nil {
		t.st.processed[consumer] = map[uuid.UUID]bool{}
	}
	if t.st.processed[consumer][eventID] {
		return false, nil
	}
	t.st.processed[consumer][eventID] = true
	return true, nil
}

// ScheduleProcessPhoto implements port.Tx.
func (t *memoryTx) ScheduleProcessPhoto(_ context.Context, listingID uuid.UUID, dueAt time.Time) (bool, error) {
	if err := t.check("ScheduleProcessPhoto"); err != nil {
		return false, err
	}
	for _, tm := range t.st.timers {
		if tm.Kind == model.TimerProcessPhoto && tm.SubjectID == listingID && !tm.Cancelled {
			return false, nil
		}
	}
	t.st.timers = append(t.st.timers, Timer{Kind: model.TimerProcessPhoto, SubjectID: listingID, DueAt: dueAt})
	return true, nil
}

// ScheduleAutoClose implements port.Tx.
func (t *memoryTx) ScheduleAutoClose(_ context.Context, listingID uuid.UUID, dueAt time.Time) (bool, error) {
	if err := t.check("ScheduleAutoClose"); err != nil {
		return false, err
	}
	for _, tm := range t.st.timers {
		if tm.Kind == model.TimerAutoClose && tm.SubjectID == listingID && !tm.Cancelled {
			return false, nil
		}
	}
	t.st.timers = append(t.st.timers, Timer{Kind: model.TimerAutoClose, SubjectID: listingID, DueAt: dueAt})
	return true, nil
}

// CancelAutoClose implements port.Tx.
func (t *memoryTx) CancelAutoClose(_ context.Context, listingID uuid.UUID, _ time.Time) (int64, error) {
	if err := t.check("CancelAutoClose"); err != nil {
		return 0, err
	}
	var n int64
	for i := range t.st.timers {
		if t.st.timers[i].Kind == model.TimerAutoClose && t.st.timers[i].SubjectID == listingID && !t.st.timers[i].Cancelled {
			t.st.timers[i].Cancelled = true
			n++
		}
	}
	return n, nil
}

var _ port.Store = (*MemoryStore)(nil)
var _ port.Tx = (*memoryTx)(nil)
var _ port.MemberInfo = (*FakeMembers)(nil)
var _ port.Blocks = (*FakeMembers)(nil)
var _ port.ObjectStore = (*MemoryObjects)(nil)
var _ port.ImageProcessor = (PassthroughImages{})
