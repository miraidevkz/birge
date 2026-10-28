// Package emergencytest holds in-memory fakes of the emergency ports for unit
// tests. The fakes are for tests only: FakeSealer does not encrypt, and the
// other-context fakes (members, blocks, acquaintances) stand in for adapters
// that are wired later.
package emergencytest

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

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
)

// Timer is a recorded auto-close or plan-link purge timer.
type Timer struct {
	DueAt     time.Time
	Cancelled bool
}

// state is everything MemoryStore keeps; a transaction works on a copy.
type state struct {
	sos       map[uuid.UUID]model.Sos
	responses map[uuid.UUID]model.SosResponse
	cards     map[uuid.UUID]model.ChildCard
	contacts  map[uuid.UUID]model.TrustedContact
	sends     map[uuid.UUID]model.PlanSend
	links     map[uuid.UUID]model.PlanLink
	purges    map[uuid.UUID]Timer
	events    []model.Event
	timers    map[uuid.UUID]Timer
	audit     []model.AuditEntry
}

func (s *state) clone() *state {
	return &state{
		sos:       maps.Clone(s.sos),
		responses: maps.Clone(s.responses),
		cards:     maps.Clone(s.cards),
		contacts:  maps.Clone(s.contacts),
		sends:     maps.Clone(s.sends),
		links:     maps.Clone(s.links),
		purges:    maps.Clone(s.purges),
		events:    slices.Clone(s.events),
		timers:    maps.Clone(s.timers),
		audit:     slices.Clone(s.audit),
	}
}

// MemoryStore is an in-memory port.Store. Transactions are serialised and
// atomic: a failing transaction leaves no trace.
type MemoryStore struct {
	mu sync.Mutex
	st *state
	// FailOn, when set, is called before each Tx method with its name (for
	// Publish: "Publish:<event type>"); a non-nil result is returned by that
	// method (to test rollbacks).
	FailOn func(method string) error
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{st: &state{
		sos:       map[uuid.UUID]model.Sos{},
		responses: map[uuid.UUID]model.SosResponse{},
		cards:     map[uuid.UUID]model.ChildCard{},
		contacts:  map[uuid.UUID]model.TrustedContact{},
		sends:     map[uuid.UUID]model.PlanSend{},
		links:     map[uuid.UUID]model.PlanLink{},
		purges:    map[uuid.UUID]Timer{},
		timers:    map[uuid.UUID]Timer{},
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

// Deliveries returns the committed notify.DeliveryRequested payloads.
func (m *MemoryStore) Deliveries() []model.DeliveryRequestedData {
	var out []model.DeliveryRequestedData
	for _, e := range m.EventsOfType(model.EventDeliveryRequested) {
		out = append(out, e.Data.(model.DeliveryRequestedData))
	}
	return out
}

// Timer returns the auto-close timer of the SOS.
func (m *MemoryStore) Timer(sosID uuid.UUID) (Timer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.st.timers[sosID]
	return t, ok
}

// PlanLinks returns the committed external plan links.
func (m *MemoryStore) PlanLinks() []model.PlanLink {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.PlanLink, 0, len(m.st.links))
	for _, l := range m.st.links {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// PurgeTimer returns the emergency.PlanLinkPurge timer of the link.
func (m *MemoryStore) PurgeTimer(linkID uuid.UUID) (Timer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.st.purges[linkID]
	return t, ok
}

// Audit returns the committed audit entries.
func (m *MemoryStore) Audit() []model.AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.st.audit)
}

// Sos returns a committed SOS.
func (m *MemoryStore) Sos(id uuid.UUID) (model.Sos, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.st.sos[id]
	return s, ok
}

// SosCount returns how many SOS are stored.
func (m *MemoryStore) SosCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.st.sos)
}

// Contact returns a committed trusted contact.
func (m *MemoryStore) Contact(id uuid.UUID) (model.TrustedContact, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.st.contacts[id]
	return c, ok
}

// Card returns a committed child card.
func (m *MemoryStore) Card(memberID uuid.UUID) (model.ChildCard, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.st.cards[memberID]
	return c, ok
}

// PutSos stores an SOS directly (test setup); Text must already be sealed.
func (m *MemoryStore) PutSos(s model.Sos) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.sos[s.ID] = s
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

// Audit implements port.Auditor.
func (t *memoryTx) Audit(_ context.Context, e model.AuditEntry) error {
	if err := t.fail("Audit"); err != nil {
		return err
	}
	t.st.audit = append(t.st.audit, e)
	return nil
}

// Lock implements port.Tx (transactions are serialised already).
func (t *memoryTx) Lock(_ context.Context, _ string, _ uuid.UUID) error { return t.fail("Lock") }

// CountSosSince implements port.Tx.
func (t *memoryTx) CountSosSince(_ context.Context, memberID uuid.UUID, since time.Time) (int, error) {
	if err := t.fail("CountSosSince"); err != nil {
		return 0, err
	}
	n := 0
	for _, s := range t.st.sos {
		if s.MemberID == memberID && s.CreatedAt.After(since) {
			n++
		}
	}
	return n, nil
}

// InsertSos implements port.Tx.
func (t *memoryTx) InsertSos(_ context.Context, s model.Sos) (model.Sos, bool, error) {
	if err := t.fail("InsertSos"); err != nil {
		return model.Sos{}, false, err
	}
	for _, cur := range t.st.sos {
		if cur.MemberID == s.MemberID && cur.IdempotencyKey == s.IdempotencyKey {
			return model.Sos{}, false, nil
		}
	}
	s.Status = model.SosOpen
	t.st.sos[s.ID] = s
	return s, true, nil
}

// GetSosByIdempotencyKey implements port.Tx.
func (t *memoryTx) GetSosByIdempotencyKey(_ context.Context, memberID, key uuid.UUID) (model.Sos, error) {
	if err := t.fail("GetSosByIdempotencyKey"); err != nil {
		return model.Sos{}, err
	}
	for _, cur := range t.st.sos {
		if cur.MemberID == memberID && cur.IdempotencyKey == key {
			return t.withCount(cur), nil
		}
	}
	return model.Sos{}, model.ErrNotFound
}

func (t *memoryTx) withCount(s model.Sos) model.Sos {
	s.ResponseCount = 0
	for _, r := range t.st.responses {
		if r.SosID == s.ID {
			s.ResponseCount++
		}
	}
	return s
}

// GetSos implements port.Tx.
func (t *memoryTx) GetSos(_ context.Context, id uuid.UUID, _ bool) (model.Sos, error) {
	if err := t.fail("GetSos"); err != nil {
		return model.Sos{}, err
	}
	s, ok := t.st.sos[id]
	if !ok {
		return model.Sos{}, model.ErrNotFound
	}
	return t.withCount(s), nil
}

// ListMySos implements port.Tx.
func (t *memoryTx) ListMySos(_ context.Context, memberID uuid.UUID, limit int32) ([]model.Sos, error) {
	if err := t.fail("ListMySos"); err != nil {
		return nil, err
	}
	var out []model.Sos
	for _, s := range t.st.sos {
		if s.MemberID == memberID {
			out = append(out, t.withCount(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return newer(out[i].CreatedAt, out[i].ID, out[j].CreatedAt, out[j].ID) })
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

func newer(a time.Time, aid uuid.UUID, b time.Time, bid uuid.UUID) bool {
	if !a.Equal(b) {
		return a.After(b)
	}
	return bytes.Compare(aid[:], bid[:]) > 0
}

// ShareSos implements port.Tx with the same conditions as the SQL update.
func (t *memoryTx) ShareSos(_ context.Context, id, districtID uuid.UUID, at time.Time) (model.Sos, bool, error) {
	if err := t.fail("ShareSos"); err != nil {
		return model.Sos{}, false, err
	}
	s, ok := t.st.sos[id]
	if !ok || s.Status != model.SosOpen || s.SharedAt != nil || s.Type == model.SosChildcare {
		return model.Sos{}, false, nil
	}
	d, a := districtID, at
	s.DistrictID, s.SharedAt = &d, &a
	t.st.sos[id] = s
	return s, true, nil
}

// CloseSos implements port.Tx.
func (t *memoryTx) CloseSos(_ context.Context, id uuid.UUID, reason string, at time.Time) (model.Sos, bool, error) {
	if err := t.fail("CloseSos"); err != nil {
		return model.Sos{}, false, err
	}
	s, ok := t.st.sos[id]
	if !ok || s.Status != model.SosOpen {
		return model.Sos{}, false, nil
	}
	r, a := reason, at
	s.Status, s.ClosedAt, s.CloseReason = model.SosClosed, &a, &r
	t.st.sos[id] = s
	return s, true, nil
}

// ListSosFeed implements port.Tx with the same filter as the SQL query.
func (t *memoryTx) ListSosFeed(_ context.Context, f model.FeedFilter) ([]model.FeedRow, error) {
	if err := t.fail("ListSosFeed"); err != nil {
		return nil, err
	}
	var rows []model.Sos
	for _, s := range t.st.sos {
		if s.DistrictID == nil || *s.DistrictID != f.DistrictID || s.Status != model.SosOpen || s.SharedAt == nil ||
			s.Type == model.SosChildcare || s.MemberID == f.ViewerID || !s.ClosesAt.After(f.Now) {
			continue
		}
		if f.CursorCreatedAt != nil && !newer(*f.CursorCreatedAt, *f.CursorID, s.CreatedAt, s.ID) {
			continue
		}
		rows = append(rows, s)
	}
	sort.Slice(rows, func(i, j int) bool { return newer(rows[i].CreatedAt, rows[i].ID, rows[j].CreatedAt, rows[j].ID) })
	if len(rows) > int(f.Limit) {
		rows = rows[:f.Limit]
	}
	out := make([]model.FeedRow, 0, len(rows))
	for _, s := range rows {
		responded := false
		for _, r := range t.st.responses {
			if r.SosID == s.ID && r.ResponderID == f.ViewerID {
				responded = true
			}
		}
		out = append(out, model.FeedRow{ID: s.ID, AuthorID: s.MemberID, Type: s.Type, Text: s.Text, CreatedAt: s.CreatedAt, RespondedByMe: responded})
	}
	return out, nil
}

// InsertSosResponse implements port.Tx.
func (t *memoryTx) InsertSosResponse(_ context.Context, sosID, responderID uuid.UUID, at time.Time) (model.SosResponse, bool, error) {
	if err := t.fail("InsertSosResponse"); err != nil {
		return model.SosResponse{}, false, err
	}
	for _, r := range t.st.responses {
		if r.SosID == sosID && r.ResponderID == responderID {
			return model.SosResponse{}, false, nil
		}
	}
	r := model.SosResponse{ID: uuid.Must(uuid.NewV7()), SosID: sosID, ResponderID: responderID, CreatedAt: at}
	t.st.responses[r.ID] = r
	return r, true, nil
}

// ListSosResponses implements port.Tx.
func (t *memoryTx) ListSosResponses(_ context.Context, sosID uuid.UUID, limit int32) ([]model.SosResponse, error) {
	if err := t.fail("ListSosResponses"); err != nil {
		return nil, err
	}
	var out []model.SosResponse
	for _, r := range t.st.responses {
		if r.SosID == sosID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return newer(out[j].CreatedAt, out[j].ID, out[i].CreatedAt, out[i].ID) })
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

// GetSosResponse implements port.Tx.
func (t *memoryTx) GetSosResponse(_ context.Context, id uuid.UUID) (model.SosResponse, error) {
	if err := t.fail("GetSosResponse"); err != nil {
		return model.SosResponse{}, err
	}
	r, ok := t.st.responses[id]
	if !ok {
		return model.SosResponse{}, model.ErrNotFound
	}
	return r, nil
}

// AcceptSosResponse implements port.Tx.
func (t *memoryTx) AcceptSosResponse(_ context.Context, id uuid.UUID, at time.Time) (model.SosResponse, bool, error) {
	if err := t.fail("AcceptSosResponse"); err != nil {
		return model.SosResponse{}, false, err
	}
	r, ok := t.st.responses[id]
	if !ok || r.AcceptedAt != nil {
		return model.SosResponse{}, false, nil
	}
	a := at
	r.AcceptedAt = &a
	t.st.responses[id] = r
	return r, true, nil
}

// UpsertChildCard implements port.Tx.
func (t *memoryTx) UpsertChildCard(_ context.Context, memberID uuid.UUID, sealed []byte, at time.Time) error {
	if err := t.fail("UpsertChildCard"); err != nil {
		return err
	}
	c := t.st.cards[memberID]
	c.MemberID, c.Text, c.UpdatedAt = memberID, bytes.Clone(sealed), at
	t.st.cards[memberID] = c
	return nil
}

// GetChildCard implements port.Tx.
func (t *memoryTx) GetChildCard(_ context.Context, memberID uuid.UUID) (model.ChildCard, error) {
	if err := t.fail("GetChildCard"); err != nil {
		return model.ChildCard{}, err
	}
	c, ok := t.st.cards[memberID]
	if !ok {
		return model.ChildCard{}, model.ErrNotFound
	}
	return c, nil
}

// MarkChildCardSent implements port.Tx.
func (t *memoryTx) MarkChildCardSent(_ context.Context, memberID uuid.UUID, at time.Time) error {
	if err := t.fail("MarkChildCardSent"); err != nil {
		return err
	}
	if c, ok := t.st.cards[memberID]; ok {
		a := at
		c.LastSentAt = &a
		t.st.cards[memberID] = c
	}
	return nil
}

// InsertTrustedContact implements port.Tx with the table's unique indexes.
func (t *memoryTx) InsertTrustedContact(_ context.Context, c model.TrustedContact) (model.TrustedContact, bool, error) {
	if err := t.fail("InsertTrustedContact"); err != nil {
		return model.TrustedContact{}, false, err
	}
	for _, cur := range t.st.contacts {
		if cur.OwnerID != c.OwnerID {
			continue
		}
		if cur.IdempotencyKey == c.IdempotencyKey {
			return model.TrustedContact{}, false, nil
		}
		if cur.Active() && cur.Kind == model.ContactMember && c.Kind == model.ContactMember &&
			*cur.ContactMemberID == *c.ContactMemberID {
			return model.TrustedContact{}, false, nil
		}
	}
	t.st.contacts[c.ID] = c
	return c, true, nil
}

// GetTrustedContactByIdempotencyKey implements port.Tx.
func (t *memoryTx) GetTrustedContactByIdempotencyKey(_ context.Context, ownerID, key uuid.UUID) (model.TrustedContact, error) {
	if err := t.fail("GetTrustedContactByIdempotencyKey"); err != nil {
		return model.TrustedContact{}, err
	}
	for _, c := range t.st.contacts {
		if c.OwnerID == ownerID && c.IdempotencyKey == key {
			return c, nil
		}
	}
	return model.TrustedContact{}, model.ErrNotFound
}

// GetTrustedContact implements port.Tx.
func (t *memoryTx) GetTrustedContact(_ context.Context, id uuid.UUID) (model.TrustedContact, error) {
	if err := t.fail("GetTrustedContact"); err != nil {
		return model.TrustedContact{}, err
	}
	c, ok := t.st.contacts[id]
	if !ok {
		return model.TrustedContact{}, model.ErrNotFound
	}
	return c, nil
}

// ListActiveTrustedContacts implements port.Tx.
func (t *memoryTx) ListActiveTrustedContacts(_ context.Context, ownerID uuid.UUID) ([]model.TrustedContact, error) {
	if err := t.fail("ListActiveTrustedContacts"); err != nil {
		return nil, err
	}
	var out []model.TrustedContact
	for _, c := range t.st.contacts {
		if c.OwnerID == ownerID && c.Active() {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return newer(out[j].CreatedAt, out[j].ID, out[i].CreatedAt, out[i].ID) })
	return out, nil
}

// ConfirmTrustedContact implements port.Tx.
func (t *memoryTx) ConfirmTrustedContact(_ context.Context, id, memberID uuid.UUID, at time.Time) (model.TrustedContact, bool, error) {
	if err := t.fail("ConfirmTrustedContact"); err != nil {
		return model.TrustedContact{}, false, err
	}
	c, ok := t.st.contacts[id]
	if !ok || c.Kind != model.ContactMember || c.ContactMemberID == nil || *c.ContactMemberID != memberID ||
		c.Status != model.ContactInvited || !c.Active() {
		return model.TrustedContact{}, false, nil
	}
	a := at
	c.Status, c.ConfirmedAt = model.ContactConfirmed, &a
	t.st.contacts[id] = c
	return c, true, nil
}

// RemoveTrustedContact implements port.Tx.
func (t *memoryTx) RemoveTrustedContact(_ context.Context, id, ownerID uuid.UUID, at time.Time) (bool, error) {
	if err := t.fail("RemoveTrustedContact"); err != nil {
		return false, err
	}
	c, ok := t.st.contacts[id]
	if !ok || c.OwnerID != ownerID || !c.Active() {
		return false, nil
	}
	a := at
	c.RemovedAt = &a
	t.st.contacts[id] = c
	return true, nil
}

// MarkContactsPlanSent implements port.Tx.
func (t *memoryTx) MarkContactsPlanSent(_ context.Context, ownerID uuid.UUID, at time.Time) (int64, error) {
	if err := t.fail("MarkContactsPlanSent"); err != nil {
		return 0, err
	}
	var n int64
	for id, c := range t.st.contacts {
		if c.OwnerID == ownerID && c.Confirmed() {
			a := at
			c.PlanSentAt = &a
			t.st.contacts[id] = c
			n++
		}
	}
	return n, nil
}

// GetSharedPlanContact implements port.Tx.
func (t *memoryTx) GetSharedPlanContact(_ context.Context, ownerID, memberID uuid.UUID) (model.TrustedContact, error) {
	if err := t.fail("GetSharedPlanContact"); err != nil {
		return model.TrustedContact{}, err
	}
	for _, c := range t.st.contacts {
		if c.OwnerID == ownerID && c.Kind == model.ContactMember && c.ContactMemberID != nil &&
			*c.ContactMemberID == memberID && c.Confirmed() {
			return c, nil
		}
	}
	return model.TrustedContact{}, model.ErrNotFound
}

// InsertPlanSend implements port.Tx.
func (t *memoryTx) InsertPlanSend(_ context.Context, p model.PlanSend) (model.PlanSend, bool, error) {
	if err := t.fail("InsertPlanSend"); err != nil {
		return model.PlanSend{}, false, err
	}
	for _, cur := range t.st.sends {
		if cur.MemberID == p.MemberID && cur.IdempotencyKey == p.IdempotencyKey {
			return model.PlanSend{}, false, nil
		}
	}
	t.st.sends[p.ID] = p
	return p, true, nil
}

// GetPlanSendByIdempotencyKey implements port.Tx.
func (t *memoryTx) GetPlanSendByIdempotencyKey(_ context.Context, memberID, key uuid.UUID) (model.PlanSend, error) {
	if err := t.fail("GetPlanSendByIdempotencyKey"); err != nil {
		return model.PlanSend{}, err
	}
	for _, p := range t.st.sends {
		if p.MemberID == memberID && p.IdempotencyKey == key {
			return p, nil
		}
	}
	return model.PlanSend{}, model.ErrNotFound
}

// CountPlanSendsSince implements port.Tx.
func (t *memoryTx) CountPlanSendsSince(_ context.Context, memberID uuid.UUID, since time.Time) (int, error) {
	if err := t.fail("CountPlanSendsSince"); err != nil {
		return 0, err
	}
	n := 0
	for _, p := range t.st.sends {
		if p.MemberID == memberID && p.SentAt.After(since) {
			n++
		}
	}
	return n, nil
}

// Publish implements port.Tx.
func (t *memoryTx) Publish(_ context.Context, _ time.Time, e model.Event) error {
	if err := t.fail("Publish:" + e.Type); err != nil {
		return err
	}
	t.st.events = append(t.st.events, e)
	return nil
}

// ScheduleAutoClose implements port.Tx (dedup on an active timer).
func (t *memoryTx) ScheduleAutoClose(_ context.Context, sosID uuid.UUID, dueAt time.Time) (bool, error) {
	if err := t.fail("ScheduleAutoClose"); err != nil {
		return false, err
	}
	if cur, ok := t.st.timers[sosID]; ok && !cur.Cancelled {
		return false, nil
	}
	t.st.timers[sosID] = Timer{DueAt: dueAt}
	return true, nil
}

// CancelAutoClose implements port.Tx.
func (t *memoryTx) CancelAutoClose(_ context.Context, sosID uuid.UUID, _ time.Time) (int64, error) {
	if err := t.fail("CancelAutoClose"); err != nil {
		return 0, err
	}
	cur, ok := t.st.timers[sosID]
	if !ok || cur.Cancelled {
		return 0, nil
	}
	cur.Cancelled = true
	t.st.timers[sosID] = cur
	return 1, nil
}

// InsertPlanLink implements port.Tx with the unique token hash.
func (t *memoryTx) InsertPlanLink(_ context.Context, l model.PlanLink) error {
	if err := t.fail("InsertPlanLink"); err != nil {
		return err
	}
	for _, cur := range t.st.links {
		if bytes.Equal(cur.TokenHash, l.TokenHash) {
			return errors.New("emergencytest: duplicate token hash")
		}
	}
	l.TokenHash = bytes.Clone(l.TokenHash)
	t.st.links[l.ID] = l
	return nil
}

func (t *memoryTx) revokeLinks(match func(model.PlanLink) bool, at time.Time) int64 {
	var n int64
	for id, l := range t.st.links {
		if l.RevokedAt == nil && match(l) {
			at := at
			l.RevokedAt = &at
			t.st.links[id] = l
			n++
		}
	}
	return n
}

// RevokeContactPlanLinks implements port.Tx.
func (t *memoryTx) RevokeContactPlanLinks(_ context.Context, contactID uuid.UUID, at time.Time) (int64, error) {
	if err := t.fail("RevokeContactPlanLinks"); err != nil {
		return 0, err
	}
	return t.revokeLinks(func(l model.PlanLink) bool { return l.ContactID == contactID }, at), nil
}

// RevokeOwnerPlanLinks implements port.Tx.
func (t *memoryTx) RevokeOwnerPlanLinks(_ context.Context, ownerID uuid.UUID, at time.Time) (int64, error) {
	if err := t.fail("RevokeOwnerPlanLinks"); err != nil {
		return 0, err
	}
	return t.revokeLinks(func(l model.PlanLink) bool { return l.OwnerID == ownerID }, at), nil
}

// GetLivePlanLink implements port.Tx with the same filter as the SQL query.
func (t *memoryTx) GetLivePlanLink(_ context.Context, tokenHash []byte, now time.Time) (model.PlanLink, error) {
	if err := t.fail("GetLivePlanLink"); err != nil {
		return model.PlanLink{}, err
	}
	for _, l := range t.st.links {
		if bytes.Equal(l.TokenHash, tokenHash) && l.RevokedAt == nil && l.ExpiresAt.After(now) {
			return l, nil
		}
	}
	return model.PlanLink{}, model.ErrNotFound
}

// DeleteExpiredPlanLink implements port.Tx.
func (t *memoryTx) DeleteExpiredPlanLink(_ context.Context, linkID uuid.UUID, cutoff time.Time) (int64, error) {
	if err := t.fail("DeleteExpiredPlanLink"); err != nil {
		return 0, err
	}
	l, ok := t.st.links[linkID]
	if !ok || l.ExpiresAt.After(cutoff) {
		return 0, nil
	}
	delete(t.st.links, linkID)
	return 1, nil
}

// SchedulePlanLinkPurge implements port.Tx (dedup on an active timer).
func (t *memoryTx) SchedulePlanLinkPurge(_ context.Context, linkID uuid.UUID, dueAt time.Time) (bool, error) {
	if err := t.fail("SchedulePlanLinkPurge"); err != nil {
		return false, err
	}
	if cur, ok := t.st.purges[linkID]; ok && !cur.Cancelled {
		return false, nil
	}
	t.st.purges[linkID] = Timer{DueAt: dueAt}
	return true, nil
}

// FakeSealer "seals" by prefixing the subject and associated data, so tests
// can check that a ciphertext opens only for its own subject and row. It
// does not encrypt.
type FakeSealer struct {
	// Err, when set, is returned by Seal and Open.
	Err error
}

var errFakeOpen = errors.New("emergencytest: ciphertext does not match subject or associated data")

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

// pair is an unordered pair of members.
type pair struct{ a, b uuid.UUID }

func pairOf(a, b uuid.UUID) pair {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	return pair{a, b}
}

// FakeMembers is an in-memory port.MemberInfo, port.Blocks and
// port.Acquaintances: the membership and circle data a test sets up.
type FakeMembers struct {
	mu       sync.Mutex
	profiles map[uuid.UUID]model.Profile
	blocks   map[pair]bool
	met      map[pair]bool
	// Err, when set, is returned by every call.
	Err error
}

// NewFakeMembers returns an empty FakeMembers.
func NewFakeMembers() *FakeMembers {
	return &FakeMembers{profiles: map[uuid.UUID]model.Profile{}, blocks: map[pair]bool{}, met: map[pair]bool{}}
}

// Put sets a member's profile.
func (f *FakeMembers) Put(id uuid.UUID, p model.Profile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles[id] = p
}

// Block records that a blocked b (blocks hide both ways).
func (f *FakeMembers) Block(a, b uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocks[pairOf(a, b)] = true
}

// Meet records that a and b met in person.
func (f *FakeMembers) Meet(a, b uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.met[pairOf(a, b)] = true
}

// Profiles implements port.MemberInfo.
func (f *FakeMembers) Profiles(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]model.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
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
	if f.Err != nil {
		return false, f.Err
	}
	return f.blocks[pairOf(a, b)], nil
}

// Met implements port.Acquaintances.
func (f *FakeMembers) Met(_ context.Context, a, b uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return false, f.Err
	}
	return f.met[pairOf(a, b)], nil
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
