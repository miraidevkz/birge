package session

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/platform/auth"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

const (
	validToken   = "tok-valid-0000000000"
	expiredToken = "tok-expired-00000000"
	inactiveTok  = "tok-inactive-0000000"
	aal2Token    = "tok-aal2-00000000000"
)

var identity = uuid.MustParse("00000000-0000-4000-8000-000000000001")

// fakeKratos answers whoami for a few fixed tokens and counts calls.
type fakeKratos struct {
	srv    *httptest.Server
	calls  atomic.Int64
	now    func() time.Time
	tokens sync.Map // tokens seen in X-Session-Token
	other  atomic.Int64
}

func newFakeKratos(t *testing.T, now func() time.Time) *fakeKratos {
	t.Helper()
	k := &fakeKratos{now: now}
	k.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sessions/whoami" {
			k.other.Add(1)
			http.NotFound(w, r)
			return
		}
		k.calls.Add(1)
		tok := r.Header.Get("X-Session-Token")
		k.tokens.Store(tok, true)
		if r.Header.Get("Authorization") != "" {
			k.other.Add(1)
		}
		exp := k.now().Add(time.Hour)
		active, aal := true, "aal1"
		switch tok {
		case validToken:
		case aal2Token:
			aal = "aal2"
		case expiredToken:
			exp = k.now().Add(-time.Minute)
		case inactiveTok:
			active = false
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":401}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"s1","active":%t,"expires_at":%q,"authenticator_assurance_level":%q,"identity":{"id":%q}}`,
			active, exp.Format(time.RFC3339Nano), aal, identity)
	}))
	t.Cleanup(k.srv.Close)
	return k
}

type harness struct {
	kratos *fakeKratos
	clock  *fakeClock
	mw     *Middleware
	h      http.Handler
	logs   *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clk := &fakeClock{now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	k := newFakeKratos(t, clk.Now)
	logs := &bytes.Buffer{}
	mw, err := New(Config{
		KratosPublicURL: k.srv.URL,
		Clock:           clk,
		Logger:          slog.New(slog.NewTextHandler(logs, nil)),
		Anonymous:       []string{"GET /v1/meta/client-config", "GET /v1/reference/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.FromContext(r.Context())
		if !ok {
			_, _ = w.Write([]byte("anonymous"))
			return
		}
		_, _ = fmt.Fprintf(w, "%s mfa=%t", p.IdentityID, p.MFA)
	})
	return &harness{kratos: k, clock: clk, mw: mw, h: mw.Wrap(next), logs: logs}
}

func (h *harness) do(t *testing.T, method, path, authz string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

func TestMiddleware(t *testing.T) {
	tests := []struct {
		name, method, path, authz string
		wantStatus                int
		wantBody                  string
		wantCalls                 int64
	}{
		{"valid", "GET", "/v1/members/me", "Bearer " + validToken, 200, identity.String() + " mfa=false", 1},
		{"scheme case-insensitive", "GET", "/v1/members/me", "bearer " + validToken, 200, identity.String(), 1},
		{"aal2 is mfa", "GET", "/v1/members/me", "Bearer " + aal2Token, 200, "mfa=true", 1},
		{"unknown token", "GET", "/v1/members/me", "Bearer tok-unknown", 401, "unauthenticated", 1},
		{"expired session", "GET", "/v1/members/me", "Bearer " + expiredToken, 401, "unauthenticated", 1},
		{"inactive session", "GET", "/v1/members/me", "Bearer " + inactiveTok, 401, "unauthenticated", 1},
		{"no header", "GET", "/v1/members/me", "", 401, "unauthenticated", 0},
		{"basic scheme", "GET", "/v1/members/me", "Basic dXNlcjpwYXNz", 401, "unauthenticated", 0},
		{"empty token", "GET", "/v1/members/me", "Bearer ", 401, "unauthenticated", 0},
		{"control char", "GET", "/v1/members/me", "Bearer a\tb", 401, "unauthenticated", 0},
		{"too long", "GET", "/v1/members/me", "Bearer " + strings.Repeat("a", maxTokenLen+1), 401, "unauthenticated", 0},
		{"anonymous exact", "GET", "/v1/meta/client-config", "", 200, "anonymous", 0},
		{"anonymous prefix", "GET", "/v1/reference/districts", "", 200, "anonymous", 0},
		{"anonymous ignores token", "GET", "/v1/reference/needs", "Bearer tok-unknown", 200, "anonymous", 0},
		{"anonymous method only", "POST", "/v1/meta/client-config", "", 401, "unauthenticated", 0},
		{"anonymous no redirect bypass", "GET", "/v1/reference/../members/me", "", 401, "unauthenticated", 0},
		{"anonymous no slash redirect", "GET", "/v1/reference", "", 401, "unauthenticated", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.do(t, tt.method, tt.path, tt.authz)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body, tt.wantBody)
			}
			if rec.Code == 401 && rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("401 without WWW-Authenticate")
			}
			if got := h.kratos.calls.Load(); got != tt.wantCalls {
				t.Errorf("kratos calls = %d, want %d", got, tt.wantCalls)
			}
			if h.kratos.other.Load() != 0 {
				t.Error("kratos got an unexpected request or an Authorization header")
			}
		})
	}
}

func TestCacheHit(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		if rec := h.do(t, "GET", "/v1/members/me", "Bearer "+validToken); rec.Code != 200 {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	if got := h.kratos.calls.Load(); got != 1 {
		t.Fatalf("kratos calls = %d, want 1 (cached)", got)
	}
	// After the TTL the session is validated again.
	h.clock.advance(61 * time.Second)
	if rec := h.do(t, "GET", "/v1/members/me", "Bearer "+validToken); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := h.kratos.calls.Load(); got != 2 {
		t.Fatalf("kratos calls = %d, want 2 after TTL", got)
	}
	// Invalid tokens are not cached as valid.
	h.do(t, "GET", "/v1/members/me", "Bearer tok-unknown")
	if rec := h.do(t, "GET", "/v1/members/me", "Bearer tok-unknown"); rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestKratosDown(t *testing.T) {
	h := newHarness(t)
	// Warm the cache, then take Kratos down.
	h.do(t, "GET", "/v1/members/me", "Bearer "+validToken)
	h.kratos.srv.Close()

	if rec := h.do(t, "GET", "/v1/members/me", "Bearer "+validToken); rec.Code != 200 {
		t.Fatalf("cached session: status = %d, want 200", rec.Code)
	}
	rec := h.do(t, "GET", "/v1/members/me", "Bearer "+aal2Token)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "auth_unavailable") {
		t.Fatalf("status = %d body %s, want 503 auth_unavailable", rec.Code, rec.Body)
	}
	if strings.Contains(h.logs.String(), aal2Token) {
		t.Fatal("token logged")
	}
}

func TestKratosServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	mw, err := New(Config{KratosPublicURL: srv.URL, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/members/me", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()
	mw.Wrap(http.NotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// A redirect from Kratos must not carry the token to another host.
func TestNoRedirectFollow(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Session-Token") != "" {
			leaked.Store(true)
		}
	}))
	defer other.Close()
	kratos := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/sessions/whoami", http.StatusTemporaryRedirect)
	}))
	defer kratos.Close()
	mw, err := New(Config{KratosPublicURL: kratos.URL, Client: &http.Client{}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/members/me", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()
	mw.Wrap(http.NotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if leaked.Load() {
		t.Fatal("token forwarded on redirect")
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Config{KratosPublicURL: "kratos"}); err == nil {
		t.Error("relative URL accepted")
	}
	if _, err := New(Config{KratosPublicURL: "http://kratos.example.invalid", Anonymous: []string{"BAD PATTERN x"}}); err == nil {
		t.Error("bad pattern accepted")
	}
}
