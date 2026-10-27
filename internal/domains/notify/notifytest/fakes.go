package notifytest

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/port"
)

// FakePush records Send calls without talking to a provider.
type FakePush struct {
	mu      sync.Mutex
	Calls   []port.PushMessage
	Err     error
	ReplyID string
}

// Send implements port.PushSender.
func (f *FakePush) Send(_ context.Context, msg port.PushMessage) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, msg)
	if f.Err != nil {
		return "", f.Err
	}
	if f.ReplyID == "" {
		return "push-fake", nil
	}
	return f.ReplyID, nil
}

// Len returns how many push sends were attempted.
func (f *FakePush) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

// FakeSMS records Send calls without talking to a provider. Phone and text are
// kept only in memory for assertions in tests; production loggers must not see them.
type FakeSMS struct {
	mu      sync.Mutex
	Calls   []port.SMSMessage
	Err     error
	ReplyID string
}

// Send implements port.SMSSender.
func (f *FakeSMS) Send(_ context.Context, msg port.SMSMessage) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, msg)
	if f.Err != nil {
		return "", f.Err
	}
	if f.ReplyID == "" {
		return "sms-fake", nil
	}
	return f.ReplyID, nil
}

// Len returns how many SMS were sent.
func (f *FakeSMS) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

// FakeRecipients resolves from in-memory maps. Phones live in PhoneByMember /
// PhoneByExternal so Resolve never returns a phone; set PhoneErr to simulate
// a transient identity-provider failure on PhoneFor.
type FakeRecipients struct {
	mu               sync.Mutex
	ByMember         map[uuid.UUID]model.ResolvedRecipient
	ByTrustedContact map[uuid.UUID]model.ResolvedRecipient
	ByStaffRole      map[string][]model.ResolvedRecipient
	PhoneByMember    map[uuid.UUID]string
	PhoneByExternal  map[uuid.UUID]string
	PhoneErr         error
}

// Resolve implements port.Recipients: kind, ref and language only.
func (f *FakeRecipients) Resolve(_ context.Context, r model.Recipient) ([]model.ResolvedRecipient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.MemberID != nil {
		if f.ByMember != nil {
			if got, ok := f.ByMember[*r.MemberID]; ok {
				return []model.ResolvedRecipient{withDefaults(got, model.RecipientKindMember, *r.MemberID)}, nil
			}
		}
		return []model.ResolvedRecipient{{
			Kind: model.RecipientKindMember,
			Ref:  *r.MemberID,
		}}, nil
	}
	if r.TrustedContactID != nil {
		got, ok := f.ByTrustedContact[*r.TrustedContactID]
		if !ok {
			return nil, model.ErrRecipientUnresolved
		}
		kind := got.Kind
		ref := got.Ref
		if kind == "" {
			kind = model.RecipientKindExternal
			ref = *r.TrustedContactID
		}
		if ref == uuid.Nil {
			ref = *r.TrustedContactID
		}
		out := got
		out.Kind = kind
		out.Ref = ref
		return []model.ResolvedRecipient{out}, nil
	}
	if r.StaffRole != "" {
		got, ok := f.ByStaffRole[r.StaffRole]
		if !ok || len(got) == 0 {
			return nil, model.ErrRecipientUnresolved
		}
		out := make([]model.ResolvedRecipient, len(got))
		for i, g := range got {
			out[i] = withDefaults(g, model.RecipientKindMember, g.Ref)
		}
		return out, nil
	}
	return nil, model.ErrRecipientUnresolved
}

// PhoneFor implements port.Recipients.
func (f *FakeRecipients) PhoneFor(_ context.Context, kind string, ref uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.PhoneErr != nil {
		return "", f.PhoneErr
	}
	switch kind {
	case model.RecipientKindMember:
		if f.PhoneByMember != nil {
			return f.PhoneByMember[ref], nil
		}
	case model.RecipientKindExternal:
		if f.PhoneByExternal != nil {
			return f.PhoneByExternal[ref], nil
		}
	}
	return "", nil
}

// SetPhoneErr sets the error PhoneFor returns (nil clears it). Safe for tests
// that flip the identity provider between down and up.
func (f *FakeRecipients) SetPhoneErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PhoneErr = err
}

func withDefaults(r model.ResolvedRecipient, kind string, ref uuid.UUID) model.ResolvedRecipient {
	if r.Kind == "" {
		r.Kind = kind
	}
	if r.Ref == uuid.Nil {
		r.Ref = ref
	}
	return r
}

var (
	_ port.PushSender = (*FakePush)(nil)
	_ port.SMSSender  = (*FakeSMS)(nil)
	_ port.Recipients = (*FakeRecipients)(nil)
)
