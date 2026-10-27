// Package chattest provides fakes of the chat ports for tests: a sealer
// that binds associated data and can shred, member profiles, blocks and
// the mentor role, author suspension, the safety crisis path, the fan-out
// bus and the WebSocket authenticator. Every name and text in tests is fake.
package chattest

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/platform/auth"
	"birge/internal/platform/crypto"
)

// CrisisWord is the fake lexicon entry that makes FakeCrisis fire.
const CrisisWord = "fakecrisisword"

// Clock is a settable clock.
type Clock struct {
	mu sync.Mutex
	T  time.Time
}

// Now returns the current fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.T
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.T = c.T.Add(d)
}

// FakeSealer "encrypts" by prefixing member and associated data; Open
// checks both, so a ciphertext moved to another row or member fails.
// Shredded members fail with crypto.ErrShredded.
type FakeSealer struct {
	mu       sync.Mutex
	shredded map[uuid.UUID]bool
}

// Seal implements port.Sealer.
func (f *FakeSealer) Seal(_ context.Context, member uuid.UUID, plaintext, ad []byte) ([]byte, error) {
	if f.isShredded(member) {
		return nil, crypto.ErrShredded
	}
	out := []byte("sealed:" + member.String() + ":" + string(ad) + ":")
	// Reverse the bytes so the plaintext does not appear in the ciphertext.
	for i := len(plaintext) - 1; i >= 0; i-- {
		out = append(out, plaintext[i]^0x5a)
	}
	return out, nil
}

// Open implements port.Sealer.
func (f *FakeSealer) Open(_ context.Context, member uuid.UUID, ciphertext, ad []byte) ([]byte, error) {
	if f.isShredded(member) {
		return nil, crypto.ErrShredded
	}
	prefix := []byte("sealed:" + member.String() + ":" + string(ad) + ":")
	if !bytes.HasPrefix(ciphertext, prefix) {
		return nil, crypto.ErrInvalidCiphertext
	}
	body := ciphertext[len(prefix):]
	out := make([]byte, 0, len(body))
	for i := len(body) - 1; i >= 0; i-- {
		out = append(out, body[i]^0x5a)
	}
	return out, nil
}

// Shred destroys the member's fake key.
func (f *FakeSealer) Shred(member uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.shredded == nil {
		f.shredded = map[uuid.UUID]bool{}
	}
	f.shredded[member] = true
}

func (f *FakeSealer) isShredded(member uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shredded[member]
}

// FakeMembers implements MemberInfo, Blocks and Mentors.
type FakeMembers struct {
	mu       sync.Mutex
	profiles map[uuid.UUID]model.Profile
	blocks   map[[2]uuid.UUID]bool
	mentors  map[uuid.UUID]bool
	// Err makes every call fail (membership down).
	Err error
}

// NewFakeMembers returns empty fake membership.
func NewFakeMembers() *FakeMembers {
	return &FakeMembers{profiles: map[uuid.UUID]model.Profile{}, blocks: map[[2]uuid.UUID]bool{}, mentors: map[uuid.UUID]bool{}}
}

// Add registers a member profile.
func (f *FakeMembers) Add(id uuid.UUID, p model.Profile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles[id] = p
}

// Block records that a blocked b.
func (f *FakeMembers) Block(a, b uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocks[[2]uuid.UUID{a, b}] = true
}

// SetMentor grants or revokes the mentor role.
func (f *FakeMembers) SetMentor(id uuid.UUID, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mentors[id] = on
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

// IsBlocked implements port.Blocks (both directions).
func (f *FakeMembers) IsBlocked(_ context.Context, a, b uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return false, f.Err
	}
	return f.blocks[[2]uuid.UUID{a, b}] || f.blocks[[2]uuid.UUID{b, a}], nil
}

// IsMentor implements port.Mentors.
func (f *FakeMembers) IsMentor(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return false, f.Err
	}
	return f.mentors[id], nil
}

// SuspendCall records one AuthorSuspender.SuspendAuthor invocation.
type SuspendCall struct {
	MemberID         uuid.UUID
	Reason           string
	ModerationItemID uuid.UUID
}

// FakeSuspender implements port.AuthorSuspender.
type FakeSuspender struct {
	mu    sync.Mutex
	Calls []SuspendCall
	Err   error
}

// SuspendAuthor implements port.AuthorSuspender.
func (f *FakeSuspender) SuspendAuthor(_ context.Context, memberID uuid.UUID, reason string, moderationItemID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, SuspendCall{MemberID: memberID, Reason: reason, ModerationItemID: moderationItemID})
	return f.Err
}

// Suspended returns a copy of recorded calls.
func (f *FakeSuspender) Suspended() []SuspendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SuspendCall(nil), f.Calls...)
}

// FakeCrisis implements port.Crisis: it fires on CrisisWord and opens one
// escalation per member (idempotent like safety's).
type FakeCrisis struct {
	mu          sync.Mutex
	Escalations map[uuid.UUID]uuid.UUID
	Sources     []string
	Calls       int
	// Err makes OpenEscalation fail (safety down).
	Err error
}

// Detect implements port.Crisis.
func (f *FakeCrisis) Detect(text, _ string) bool {
	return strings.Contains(strings.ToLower(text), CrisisWord)
}

// CrisisPhones implements port.Crisis.
func (f *FakeCrisis) CrisisPhones() []model.CrisisPhone {
	return []model.CrisisPhone{{Number: "112", Label: "Emergency services (fake label)"}}
}

// OpenEscalation implements port.Crisis.
func (f *FakeCrisis) OpenEscalation(_ context.Context, member uuid.UUID, source string) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls++
	if f.Err != nil {
		return uuid.Nil, f.Err
	}
	f.Sources = append(f.Sources, source)
	if f.Escalations == nil {
		f.Escalations = map[uuid.UUID]uuid.UUID{}
	}
	if id, ok := f.Escalations[member]; ok {
		return id, nil
	}
	id := uuid.New()
	f.Escalations[member] = id
	return id, nil
}

// SetErr sets or clears the OpenEscalation failure.
func (f *FakeCrisis) SetErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Err = err
}

// Escalation returns the member's escalation id, if any.
func (f *FakeCrisis) Escalation(member uuid.UUID) (uuid.UUID, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.Escalations[member]
	return id, ok
}

// Bus is an in-memory fan-out implementing port.Fanout and port.Bus.
type Bus struct {
	mu    sync.Mutex
	subs  map[uuid.UUID]map[int]func(model.Notice)
	next  int
	Sent  []model.Notice
	Drop  bool // when true, Publish records but delivers nothing (lost notices)
	Error error
}

// Publish implements port.Fanout; delivery is synchronous.
func (b *Bus) Publish(_ context.Context, n model.Notice) error {
	b.mu.Lock()
	if b.Error != nil {
		b.mu.Unlock()
		return b.Error
	}
	b.Sent = append(b.Sent, n)
	var fns []func(model.Notice)
	if !b.Drop {
		for _, fn := range b.subs[n.RoomID] {
			fns = append(fns, fn)
		}
	}
	b.mu.Unlock()
	for _, fn := range fns {
		fn(n)
	}
	return nil
}

// Subscribe implements port.Bus.
func (b *Bus) Subscribe(roomID uuid.UUID, fn func(model.Notice)) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[uuid.UUID]map[int]func(model.Notice){}
	}
	if b.subs[roomID] == nil {
		b.subs[roomID] = map[int]func(model.Notice){}
	}
	b.next++
	id := b.next
	b.subs[roomID][id] = fn
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs[roomID], id)
	}, nil
}

// Notices returns a copy of the published notices.
func (b *Bus) Notices() []model.Notice {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]model.Notice(nil), b.Sent...)
}

// FakeAuth accepts tokens registered with Grant.
type FakeAuth struct {
	mu     sync.Mutex
	tokens map[string]auth.Principal
}

// Grant makes token authenticate as p.
func (f *FakeAuth) Grant(token string, p auth.Principal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokens == nil {
		f.tokens = map[string]auth.Principal{}
	}
	f.tokens[token] = p
}

// Authenticate implements port.Authenticator.
func (f *FakeAuth) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.tokens[token]
	if !ok {
		return auth.Principal{}, errors.New("chattest: invalid token")
	}
	return p, nil
}
