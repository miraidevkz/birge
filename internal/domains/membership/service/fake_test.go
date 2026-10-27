package service_test

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

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/port"
	"birge/internal/platform/outbox"
)

// fakeEncryptor binds the ciphertext to the member ID so a name opened with the
// wrong member's key fails, and never stores the plaintext as is.
type fakeEncryptor struct {
	failSeal, failOpen bool
}

func (e *fakeEncryptor) Seal(_ context.Context, memberID uuid.UUID, plaintext []byte) ([]byte, error) {
	if e.failSeal {
		return nil, errors.New("seal failed")
	}
	out := append([]byte{}, memberID[:]...)
	for _, b := range plaintext {
		out = append(out, b^0x5a)
	}
	return out, nil
}

func (e *fakeEncryptor) Open(_ context.Context, memberID uuid.UUID, sealed []byte) ([]byte, error) {
	if e.failOpen {
		return nil, errors.New("open failed")
	}
	if len(sealed) < 16 || !bytes.Equal(sealed[:16], memberID[:]) {
		return nil, errors.New("wrong key")
	}
	out := make([]byte, 0, len(sealed)-16)
	for _, b := range sealed[16:] {
		out = append(out, b^0x5a)
	}
	return out, nil
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

type fakeChild struct {
	member uuid.UUID
	key    uuid.UUID
	child  model.Child
	order  int
}

type blockKey struct{ blocker, blocked uuid.UUID }

type state struct {
	members       map[uuid.UUID]port.MemberRecord
	children      map[uuid.UUID]fakeChild
	pending       map[uuid.UUID]uuid.UUID // member -> pending request
	verifications map[uuid.UUID]model.VerificationRequest
	processed     map[string]bool
	audits        int
	blocks        map[blockKey]time.Time
	events        []outbox.Event
	districts     map[uuid.UUID]uuid.UUID // district -> city
	cities        []model.City
	needs         []string
}

func (s state) clone() state {
	c := s
	c.members = maps.Clone(s.members)
	c.children = maps.Clone(s.children)
	c.pending = maps.Clone(s.pending)
	c.verifications = maps.Clone(s.verifications)
	c.processed = maps.Clone(s.processed)
	c.blocks = maps.Clone(s.blocks)
	c.events = slices.Clone(s.events)
	return c
}

// fakeStore is an in-memory port.Store. Transactions are serialized and roll
// back on error. fail makes the named method return an error.
type fakeStore struct {
	mu   sync.Mutex
	st   state
	fail map[string]error
	seq  int
}

func newFakeStore() *fakeStore {
	return &fakeStore{st: state{
		members: map[uuid.UUID]port.MemberRecord{}, children: map[uuid.UUID]fakeChild{},
		pending: map[uuid.UUID]uuid.UUID{}, verifications: map[uuid.UUID]model.VerificationRequest{},
		processed: map[string]bool{}, blocks: map[blockKey]time.Time{}, districts: map[uuid.UUID]uuid.UUID{},
	}, fail: map[string]error{}}
}

func (f *fakeStore) InTx(_ context.Context, fn func(port.Tx) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	saved := f.st.clone()
	if err := fn(&fakeTx{f}); err != nil {
		f.st = saved
		return err
	}
	return nil
}

func (f *fakeStore) memberCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.st.members)
}

func (f *fakeStore) events() []outbox.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.st.events)
}

type fakeTx struct{ f *fakeStore }

func (t *fakeTx) check(method string) error { return t.f.fail[method] }

func (t *fakeTx) withCity(r port.MemberRecord) port.MemberRecord {
	if r.DistrictID != nil {
		c := t.f.st.districts[*r.DistrictID]
		r.CityID = &c
	}
	return r
}

func (t *fakeTx) MemberByIdentity(_ context.Context, identityID uuid.UUID) (port.MemberRecord, error) {
	if err := t.check("MemberByIdentity"); err != nil {
		return port.MemberRecord{}, err
	}
	for _, m := range t.f.st.members {
		if m.IdentityID == identityID {
			return t.withCity(m), nil
		}
	}
	return port.MemberRecord{}, model.ErrMemberNotFound
}

func (t *fakeTx) MemberByID(_ context.Context, id uuid.UUID) (port.MemberRecord, error) {
	if err := t.check("MemberByID"); err != nil {
		return port.MemberRecord{}, err
	}
	m, ok := t.f.st.members[id]
	if !ok {
		return port.MemberRecord{}, model.ErrMemberNotFound
	}
	return t.withCity(m), nil
}

func (t *fakeTx) LockMember(_ context.Context, id uuid.UUID) error {
	if err := t.check("LockMember"); err != nil {
		return err
	}
	if _, ok := t.f.st.members[id]; !ok {
		return model.ErrMemberNotFound
	}
	return nil
}

func (t *fakeTx) InsertMember(_ context.Context, identityID uuid.UUID, _ time.Time) (uuid.UUID, bool, error) {
	if err := t.check("InsertMember"); err != nil {
		return uuid.Nil, false, err
	}
	for _, m := range t.f.st.members {
		if m.IdentityID == identityID {
			return uuid.Nil, false, nil
		}
	}
	id := uuid.New()
	t.f.st.members[id] = port.MemberRecord{
		ID: id, IdentityID: identityID, Lang: model.LangKK, Verification: model.VerificationNew, Roles: []string{"mom"},
	}
	return id, true, nil
}

func (t *fakeTx) UpdateProfile(_ context.Context, id uuid.UUID, p port.ProfilePatch, _ time.Time) error {
	if err := t.check("UpdateProfile"); err != nil {
		return err
	}
	m, ok := t.f.st.members[id]
	if !ok {
		return model.ErrMemberNotFound
	}
	if p.SealedName != nil {
		m.SealedName = p.SealedName
	}
	if p.Lang != nil {
		m.Lang = *p.Lang
	}
	if p.DistrictID != nil {
		m.DistrictID = p.DistrictID
	}
	t.f.st.members[id] = m
	return nil
}

func (t *fakeTx) SetVerification(_ context.Context, id uuid.UUID, v model.Verification, _ time.Time) error {
	if err := t.check("SetVerification"); err != nil {
		return err
	}
	m := t.f.st.members[id]
	m.Verification = v
	t.f.st.members[id] = m
	return nil
}

func (t *fakeTx) Children(_ context.Context, memberID uuid.UUID) ([]model.Child, error) {
	if err := t.check("Children"); err != nil {
		return nil, err
	}
	var cs []fakeChild
	for _, c := range t.f.st.children {
		if c.member == memberID {
			cs = append(cs, c)
		}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].order < cs[j].order })
	out := []model.Child{}
	for _, c := range cs {
		out = append(out, c.child)
	}
	return out, nil
}

func (t *fakeTx) CountChildren(ctx context.Context, memberID uuid.UUID) (int, error) {
	cs, err := t.Children(ctx, memberID)
	return len(cs), err
}

func (t *fakeTx) ChildByIdempotencyKey(_ context.Context, memberID, key uuid.UUID) (model.Child, error) {
	for _, c := range t.f.st.children {
		if c.member == memberID && c.key == key {
			return c.child, nil
		}
	}
	return model.Child{}, model.ErrChildNotFound
}

func (t *fakeTx) InsertChild(_ context.Context, memberID, key uuid.UUID, in model.ChildInput, _ time.Time) (model.Child, error) {
	if err := t.check("InsertChild"); err != nil {
		return model.Child{}, err
	}
	t.f.seq++
	c := model.Child{ID: uuid.New(), BirthYear: in.BirthYear, Needs: in.Needs}
	t.f.st.children[c.ID] = fakeChild{member: memberID, key: key, child: c, order: t.f.seq}
	return c, nil
}

func (t *fakeTx) UpdateChild(_ context.Context, memberID, childID uuid.UUID, in model.ChildInput, _ time.Time) (model.Child, error) {
	c, ok := t.f.st.children[childID]
	if !ok || c.member != memberID {
		return model.Child{}, model.ErrChildNotFound
	}
	c.child.BirthYear, c.child.Needs = in.BirthYear, in.Needs
	t.f.st.children[childID] = c
	return c.child, nil
}

func (t *fakeTx) DeleteChild(_ context.Context, memberID, childID uuid.UUID) (bool, error) {
	c, ok := t.f.st.children[childID]
	if !ok || c.member != memberID {
		return false, nil
	}
	delete(t.f.st.children, childID)
	return true, nil
}

func (t *fakeTx) InsertPendingVerification(_ context.Context, memberID uuid.UUID, now time.Time) (uuid.UUID, bool, error) {
	if err := t.check("InsertPendingVerification"); err != nil {
		return uuid.Nil, false, err
	}
	if _, ok := t.f.st.pending[memberID]; ok {
		return uuid.Nil, false, nil
	}
	id := uuid.New()
	t.f.st.pending[memberID] = id
	t.f.st.verifications[id] = model.VerificationRequest{
		ID: id, MemberID: memberID, Status: model.VerificationRequestPending, CreatedAt: now, UpdatedAt: now,
	}
	return id, true, nil
}

func (t *fakeTx) SetSuspension(_ context.Context, id uuid.UUID, at *time.Time, reason string, _ time.Time) error {
	if err := t.check("SetSuspension"); err != nil {
		return err
	}
	m, ok := t.f.st.members[id]
	if !ok {
		return model.ErrMemberNotFound
	}
	m.SuspendedAt, m.SuspendedReason = at, reason
	if at == nil {
		m.SuspendedReason = ""
	}
	t.f.st.members[id] = m
	return nil
}

func (t *fakeTx) GetVerificationRequest(_ context.Context, id uuid.UUID, _ bool) (model.VerificationRequest, error) {
	if err := t.check("GetVerificationRequest"); err != nil {
		return model.VerificationRequest{}, err
	}
	r, ok := t.f.st.verifications[id]
	if !ok {
		return model.VerificationRequest{}, model.ErrMemberNotFound
	}
	return r, nil
}

func (t *fakeTx) DecideVerificationRequest(_ context.Context, id uuid.UUID, status, rejectReason string, now time.Time) (bool, error) {
	if err := t.check("DecideVerificationRequest"); err != nil {
		return false, err
	}
	r, ok := t.f.st.verifications[id]
	if !ok || r.Status != model.VerificationRequestPending {
		return false, nil
	}
	r.Status, r.RejectReason, r.UpdatedAt = status, rejectReason, now
	t.f.st.verifications[id] = r
	if status != model.VerificationRequestPending {
		delete(t.f.st.pending, r.MemberID)
	}
	return true, nil
}

func (t *fakeTx) MarkProcessed(_ context.Context, consumer string, eventID uuid.UUID) (bool, error) {
	if err := t.check("MarkProcessed"); err != nil {
		return false, err
	}
	key := consumer + "|" + eventID.String()
	if t.f.st.processed[key] {
		return false, nil
	}
	t.f.st.processed[key] = true
	return true, nil
}

func (t *fakeTx) Audit(_ context.Context, _ uuid.UUID, _, _, _ string, _ uuid.UUID, _ string) error {
	if err := t.check("Audit"); err != nil {
		return err
	}
	t.f.st.audits++
	return nil
}

func (t *fakeTx) InsertBlock(_ context.Context, blocker, blocked uuid.UUID, now time.Time) (bool, error) {
	if err := t.check("InsertBlock"); err != nil {
		return false, err
	}
	k := blockKey{blocker, blocked}
	if _, ok := t.f.st.blocks[k]; ok {
		return false, nil
	}
	t.f.st.blocks[k] = now
	return true, nil
}

func (t *fakeTx) DeleteBlock(_ context.Context, blocker, blocked uuid.UUID) error {
	delete(t.f.st.blocks, blockKey{blocker, blocked})
	return nil
}

func (t *fakeTx) CountBlocks(_ context.Context, blocker uuid.UUID) (int, error) {
	n := 0
	for k := range t.f.st.blocks {
		if k.blocker == blocker {
			n++
		}
	}
	return n, nil
}

func (t *fakeTx) Blocks(_ context.Context, blocker uuid.UUID, limit int) ([]port.BlockRecord, error) {
	if err := t.check("Blocks"); err != nil {
		return nil, err
	}
	var out []port.BlockRecord
	for k, since := range t.f.st.blocks {
		if k.blocker == blocker {
			out = append(out, port.BlockRecord{Blocked: k.blocked, SealedName: t.f.st.members[k.blocked].SealedName, Since: since})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since.After(out[j].Since) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (t *fakeTx) IsBlocked(_ context.Context, a, b uuid.UUID) (bool, error) {
	if err := t.check("IsBlocked"); err != nil {
		return false, err
	}
	_, ab := t.f.st.blocks[blockKey{a, b}]
	_, ba := t.f.st.blocks[blockKey{b, a}]
	if ab || ba {
		return true, nil
	}
	if ma, ok := t.f.st.members[a]; ok && ma.SuspendedAt != nil {
		return true, nil
	}
	if mb, ok := t.f.st.members[b]; ok && mb.SuspendedAt != nil {
		return true, nil
	}
	return false, nil
}

func (t *fakeTx) ListVerifiedInDistrict(_ context.Context, districtID uuid.UUID) ([]uuid.UUID, error) {
	if err := t.check("ListVerifiedInDistrict"); err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for id, m := range t.f.st.members {
		if m.DistrictID != nil && *m.DistrictID == districtID &&
			m.Verification == model.VerificationVerified && m.SuspendedAt == nil {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func (t *fakeTx) DistrictExists(_ context.Context, id uuid.UUID) (bool, error) {
	if err := t.check("DistrictExists"); err != nil {
		return false, err
	}
	_, ok := t.f.st.districts[id]
	return ok, nil
}

func (t *fakeTx) CountKnownNeeds(_ context.Context, codes []string) (int, error) {
	n := 0
	for _, c := range codes {
		if slices.Contains(t.f.st.needs, c) {
			n++
		}
	}
	return n, nil
}

func (t *fakeTx) Cities(context.Context) ([]model.City, error) {
	if err := t.check("Cities"); err != nil {
		return nil, err
	}
	return t.f.st.cities, nil
}

func (t *fakeTx) Needs(context.Context) ([]string, error) {
	if err := t.check("Needs"); err != nil {
		return nil, err
	}
	return t.f.st.needs, nil
}

func (t *fakeTx) Publish(_ context.Context, _ time.Time, e outbox.Event) error {
	if err := t.check("Publish"); err != nil {
		return err
	}
	t.f.st.events = append(t.f.st.events, e)
	return nil
}
