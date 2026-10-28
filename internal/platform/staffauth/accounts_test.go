package staffauth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// fakeAccounts records calls; fail makes every call return an error.
type fakeAccounts struct {
	mu    sync.Mutex
	calls []Account
	fail  bool
	ctxOK bool
}

func (f *fakeAccounts) RecordStaffAccount(ctx context.Context, a Account) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, a)
	_, f.ctxOK = ctx.Deadline()
	if f.fail {
		return errors.New("store down: Alice Example")
	}
	return nil
}

func (f *fakeAccounts) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeAccounts) last() Account {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func TestAccountRecordedAndThrottled(t *testing.T) {
	const token = "tok-account-0123456789"
	acc := &fakeAccounts{}
	cfg := hydraConfig()
	cfg.Accounts = acc
	h := newHarness(t, cfg, map[string]map[string]any{
		token: hydraToken(func(m map[string]any) {
			m["exp"] = start.Add(3 * time.Hour).Unix()
			m["ext"].(map[string]any)["name"] = "  Alice\tExample "
			m["email"] = "alice@example.invalid"
		}),
	})

	if rec := h.do(t, "Bearer "+token); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if acc.count() != 1 {
		t.Fatalf("records = %d, want 1 on the first request", acc.count())
	}
	got := acc.last()
	want := Account{StaffID: StaffID(hydraIssuer, "staff:alice"), DisplayName: "Alice Example", Roles: []string{"psychologist"}, SeenAt: start}
	if got.StaffID != want.StaffID || got.DisplayName != want.DisplayName || strings.Join(got.Roles, ",") != "psychologist" || !got.SeenAt.Equal(start) {
		t.Fatalf("account = %+v, want %+v", got, want)
	}
	if !acc.ctxOK {
		t.Error("record called without a deadline")
	}

	// Within the hour: no further writes, even after the token cache expired.
	h.clock.advance(59 * time.Minute)
	for range 3 {
		if rec := h.do(t, "Bearer "+token); rec.Code != 200 {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	if acc.count() != 1 {
		t.Fatalf("records = %d, want 1 within the hour", acc.count())
	}

	// After the hour: refreshed once.
	h.clock.advance(time.Minute)
	h.do(t, "Bearer "+token)
	h.do(t, "Bearer "+token)
	if acc.count() != 2 {
		t.Fatalf("records = %d, want 2 after an hour", acc.count())
	}
	if !acc.last().SeenAt.Equal(start.Add(time.Hour)) {
		t.Errorf("seen at = %s", acc.last().SeenAt)
	}
	if strings.Contains(h.logs.String(), "Alice") || strings.Contains(h.logs.String(), "alice@") {
		t.Error("name or email logged")
	}
}

func TestAccountStoreFailureDoesNotFailRequest(t *testing.T) {
	const token = "tok-accfail-0123456789"
	acc := &fakeAccounts{fail: true}
	cfg := hydraConfig()
	cfg.Accounts = acc
	h := newHarness(t, cfg, map[string]map[string]any{
		token: hydraToken(func(m map[string]any) {
			m["exp"] = start.Add(3 * time.Hour).Unix()
			m["name"] = "Alice Example"
		}),
	})
	if rec := h.do(t, "Bearer "+token); rec.Code != 200 {
		t.Fatalf("status = %d, want 200 although the store failed: %s", rec.Code, rec.Body)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "StaffAccountRecordFailed") || !strings.Contains(logs, StaffID(hydraIssuer, "staff:alice").String()) {
		t.Errorf("failure not logged with event and staff ID: %s", logs)
	}
	if strings.Contains(logs, "Alice") || strings.Contains(logs, token) {
		t.Errorf("log carries the name, error text or token: %s", logs)
	}

	// No retry storm: the next requests within a minute do not write.
	h.clock.advance(30 * time.Second)
	h.do(t, "Bearer "+token)
	if acc.count() != 1 {
		t.Fatalf("records = %d, want 1 within the retry wait", acc.count())
	}
	// After the retry wait the store is tried again.
	h.clock.advance(31 * time.Second)
	acc.mu.Lock()
	acc.fail = false
	acc.mu.Unlock()
	if rec := h.do(t, "Bearer "+token); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if acc.count() != 2 {
		t.Fatalf("records = %d, want 2 after the retry wait", acc.count())
	}
}

func TestAccountNotRecordedWithoutMFAOrValidToken(t *testing.T) {
	acc := &fakeAccounts{}
	cfg := hydraConfig()
	cfg.Accounts = acc
	h := newHarness(t, cfg, map[string]map[string]any{
		"tok-nomfa-0123456789":   hydraToken(func(m map[string]any) { m["ext"] = map[string]any{"roles": []any{"admin"}, "name": "X"} }),
		"tok-expired-0123456789": hydraToken(func(m map[string]any) { m["exp"] = start.Add(-time.Second).Unix() }),
	})
	if rec := h.do(t, "Bearer tok-nomfa-0123456789"); rec.Code != 403 {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if rec := h.do(t, "Bearer tok-expired-0123456789"); rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	h.do(t, "Bearer tok-unknown-0123456789")
	if acc.count() != 0 {
		t.Fatalf("records = %d, want 0 for rejected requests", acc.count())
	}
}

func TestDisplayName(t *testing.T) {
	long := strings.Repeat("Ә", 100)
	tests := []struct {
		name   string
		claims claims
		want   string
	}{
		{"name", claims{"name": "Alice", "preferred_username": "alice"}, "Alice"},
		{"preferred_username fallback", claims{"preferred_username": "alice"}, "alice"},
		{"ext name (Hydra)", claims{"ext": map[string]any{"name": "Alice"}}, "Alice"},
		{"top level wins", claims{"name": "Top", "ext": map[string]any{"name": "Ext"}}, "Top"},
		{"blank name falls back", claims{"name": "   ", "preferred_username": "alice"}, "alice"},
		{"email username dropped", claims{"preferred_username": "alice@example.invalid"}, ""},
		{"email name dropped, username kept", claims{"name": "alice@example.invalid", "preferred_username": "alice"}, "alice"},
		{"email claim never read", claims{"email": "alice@example.invalid"}, ""},
		{"control characters", claims{"name": "Al\x00ice\u200b\nB"}, "Al ice B"},
		{"invalid utf-8", claims{"name": "Al\xffice"}, "Alice"},
		{"not a string", claims{"name": 42}, ""},
		{"trimmed to 80 characters", claims{"name": long}, strings.Repeat("Ә", MaxDisplayNameLen)},
		{"none", claims{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := displayName(tt.claims)
			if got != tt.want {
				t.Fatalf("displayName = %q, want %q", got, tt.want)
			}
			if utf8.RuneCountInString(got) > MaxDisplayNameLen || !utf8.ValidString(got) {
				t.Fatalf("invalid stored name %q", got)
			}
		})
	}
}

func TestAccountThrottleBound(t *testing.T) {
	th := newAccountThrottle(3)
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for _, id := range ids[:3] {
		if !th.claim(id, start) {
			t.Fatal("first claim refused")
		}
	}
	if th.claim(ids[0], start.Add(time.Minute)) {
		t.Fatal("second claim within the hour accepted")
	}
	// Full and nothing expired: the map starts over instead of growing.
	if !th.claim(ids[3], start.Add(time.Minute)) {
		t.Fatal("claim of a new ID refused when full")
	}
	if len(th.next) > 3 {
		t.Fatalf("map grew past its bound: %d", len(th.next))
	}
	// Expired slots are pruned first.
	th = newAccountThrottle(2)
	th.claim(ids[0], start)
	th.claim(ids[1], start.Add(30*time.Minute))
	th.claim(ids[2], start.Add(time.Hour))
	if _, ok := th.next[ids[1]]; !ok || len(th.next) != 2 {
		t.Fatalf("prune dropped a live slot: %v", th.next)
	}
}
