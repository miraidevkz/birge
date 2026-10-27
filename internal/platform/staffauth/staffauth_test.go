package staffauth

import (
	"bytes"
	"context"
	"encoding/json"
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
	clientID     = "birge-api"
	clientSecret = "test-client-secret-not-real"
)

// fakeIdP is an RFC 7662 introspection endpoint answering from a fixed
// token → response table. Unknown tokens are inactive, like a real provider.
type fakeIdP struct {
	srv       *httptest.Server
	calls     atomic.Int64
	violation atomic.Value // string: first protocol violation seen
	responses map[string]map[string]any
}

func newFakeIdP(t *testing.T, responses map[string]map[string]any) *fakeIdP {
	t.Helper()
	f := &fakeIdP{responses: responses}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		id, secret, ok := r.BasicAuth()
		switch {
		case r.Method != http.MethodPost || r.URL.Path != "/introspect":
			f.violation.CompareAndSwap(nil, "method or path "+r.Method+" "+r.URL.Path)
		case r.URL.RawQuery != "":
			f.violation.CompareAndSwap(nil, "query string sent")
		case r.Header.Get("Content-Type") != "application/x-www-form-urlencoded":
			f.violation.CompareAndSwap(nil, "content type")
		}
		if !ok || id != clientID || secret != clientSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp, known := f.responses[r.PostForm.Get("token")]
		if !known {
			resp = map[string]any{"active": false}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

var start = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

const (
	hydraIssuer    = "https://hydra.example.invalid/"
	keycloakIssuer = "https://keycloak.example.invalid/realms/birge"
	alice          = "8f7c1a3e-1d2b-4c5d-9e6f-0a1b2c3d4e5f"
)

// hydraToken is an introspection response shaped like Ory Hydra's: opaque
// token, "aud" array, "token_use", custom claims under "ext". Modifiers
// adjust it per case.
func hydraToken(mod func(map[string]any)) map[string]any {
	m := map[string]any{
		"active":     true,
		"sub":        "staff:alice",
		"client_id":  "birge-admin",
		"aud":        []any{clientID},
		"iss":        hydraIssuer,
		"exp":        start.Add(time.Hour).Unix(),
		"iat":        start.Unix(),
		"token_type": "Bearer",
		"token_use":  "access_token",
		"scope":      "openid offline",
		"ext":        map[string]any{"roles": []any{"psychologist", "unknown-group"}, "amr": []any{"pwd", "webauthn"}},
	}
	if mod != nil {
		mod(m)
	}
	return m
}

// keycloakToken is shaped like Keycloak's: UUID subject, "aud" string,
// "typ", realm roles under realm_access, LoA in "acr".
func keycloakToken(mod func(map[string]any)) map[string]any {
	m := map[string]any{
		"active":       true,
		"sub":          alice,
		"azp":          "birge-admin",
		"client_id":    "birge-admin",
		"aud":          "account",
		"iss":          keycloakIssuer,
		"exp":          start.Add(time.Hour).Unix(),
		"typ":          "Bearer",
		"acr":          "2",
		"realm_access": map[string]any{"roles": []any{"birge-moderator", "birge-admin", "offline_access"}},
	}
	if mod != nil {
		mod(m)
	}
	return m
}

type harness struct {
	idp   *fakeIdP
	clock *fakeClock
	mw    *Middleware
	h     http.Handler
	logs  *bytes.Buffer
}

func hydraConfig() Config {
	return Config{Issuer: hydraIssuer, Audience: clientID, RolesClaim: "ext.roles", MFAAMR: []string{"mfa", "webauthn", "hwk"}}
}

func keycloakConfig() Config {
	return Config{
		Issuer: keycloakIssuer, Audience: "birge-admin", RolesClaim: "realm_access.roles",
		RoleMap: map[string]string{"birge-moderator": RoleModerator, "birge-admin": RoleAdmin, "birge-psychologist": RolePsychologist},
		MFAACR:  []string{"2"},
	}
}

func newHarness(t *testing.T, cfg Config, responses map[string]map[string]any) *harness {
	t.Helper()
	clk := &fakeClock{now: start}
	idp := newFakeIdP(t, responses)
	logs := &bytes.Buffer{}
	cfg.IntrospectionURL = idp.srv.URL + "/introspect"
	cfg.ClientID, cfg.ClientSecret = clientID, clientSecret
	cfg.Clock = clk
	cfg.Logger = slog.New(slog.NewTextHandler(logs, nil))
	mw, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.FromContext(r.Context())
		if !ok {
			t.Error("handler reached without a principal")
			return
		}
		_, _ = fmt.Fprintf(w, "%s staff=%t roles=%s mfa=%t", p.IdentityID, p.Staff, strings.Join(p.Roles, ","), p.MFA)
	})
	return &harness{idp: idp, clock: clk, mw: mw, h: mw.Wrap(next), logs: logs}
}

func (h *harness) do(t *testing.T, authz string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/staff/escalations", nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

type tokenCase struct {
	name       string
	response   map[string]any // nil: the provider does not know the token
	authz      string         // default "Bearer tok-case"
	wantStatus int
	wantBody   string
	wantCalls  int64
}

func runCases(t *testing.T, cfg func() Config, cases []tokenCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const token = "tok-case-0123456789"
			responses := map[string]map[string]any{}
			if tc.response != nil {
				responses[token] = tc.response
			}
			h := newHarness(t, cfg(), responses)
			authz := tc.authz
			if authz == "" {
				authz = "Bearer " + token
			}
			rec := h.do(t, authz)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body, tc.wantBody)
			}
			if rec.Code == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("401 without WWW-Authenticate")
			}
			if rec.Code == http.StatusForbidden && !strings.Contains(rec.Header().Get("WWW-Authenticate"), "insufficient_user_authentication") {
				t.Errorf("403 without step-up challenge: %q", rec.Header().Get("WWW-Authenticate"))
			}
			if got := h.idp.calls.Load(); got != tc.wantCalls {
				t.Errorf("introspection calls = %d, want %d", got, tc.wantCalls)
			}
			if v := h.idp.violation.Load(); v != nil {
				t.Errorf("introspection request: %v", v)
			}
			if strings.Contains(h.logs.String(), token) {
				t.Error("token logged")
			}
		})
	}
}

func TestHydraStyle(t *testing.T) {
	derived := StaffID(hydraIssuer, "staff:alice").String()
	runCases(t, hydraConfig, []tokenCase{
		{"active with roles and mfa", hydraToken(nil), "", 200, derived + " staff=true roles=psychologist mfa=true", 1},
		{"top-level amr wins over ext", hydraToken(func(m map[string]any) { m["amr"] = []any{"pwd"} }), "", 403, "mfa_required", 1},
		{"no mfa", hydraToken(func(m map[string]any) { m["ext"] = map[string]any{"roles": []any{"psychologist"}, "amr": []any{"pwd"}} }), "", 403, "mfa_required", 1},
		{"no amr at all", hydraToken(func(m map[string]any) { m["ext"] = map[string]any{"roles": []any{"psychologist"}} }), "", 403, "mfa_required", 1},
		{"inactive", hydraToken(func(m map[string]any) { m["active"] = false }), "", 401, "unauthenticated", 1},
		{"unknown token", nil, "", 401, "unauthenticated", 1},
		{"wrong issuer", hydraToken(func(m map[string]any) { m["iss"] = "https://other.example.invalid/" }), "", 401, "unauthenticated", 1},
		{"issuer without trailing slash", hydraToken(func(m map[string]any) { m["iss"] = strings.TrimSuffix(hydraIssuer, "/") }), "", 401, "unauthenticated", 1},
		{"wrong audience", hydraToken(func(m map[string]any) { m["aud"] = []any{"other-api"} }), "", 401, "unauthenticated", 1},
		{"refresh token", hydraToken(func(m map[string]any) { m["token_use"] = "refresh_token" }), "", 401, "unauthenticated", 1},
		{"expired", hydraToken(func(m map[string]any) { m["exp"] = start.Add(-time.Second).Unix() }), "", 401, "unauthenticated", 1},
		{"no exp", hydraToken(func(m map[string]any) { delete(m, "exp") }), "", 401, "unauthenticated", 1},
		{"no sub", hydraToken(func(m map[string]any) { delete(m, "sub") }), "", 401, "unauthenticated", 1},
		{"active as string", hydraToken(func(m map[string]any) { m["active"] = "true" }), "", 401, "unauthenticated", 1},
		{"no roles", hydraToken(func(m map[string]any) { m["ext"] = map[string]any{"amr": []any{"hwk"}} }), "", 200, "roles= mfa=true", 1},
		{"no header", nil, "-", 401, "unauthenticated", 0},
		{"basic scheme", nil, "Basic dXNlcjpwYXNz", 401, "unauthenticated", 0},
	})
}

func TestKeycloakStyle(t *testing.T) {
	runCases(t, keycloakConfig, []tokenCase{
		{"active with roles and mfa", keycloakToken(nil), "", 200, alice + " staff=true roles=admin,moderator mfa=true", 1},
		{"acr below mfa", keycloakToken(func(m map[string]any) { m["acr"] = "1" }), "", 403, "mfa_required", 1},
		{"inactive", keycloakToken(func(m map[string]any) { m["active"] = false }), "", 401, "unauthenticated", 1},
		{"wrong issuer", keycloakToken(func(m map[string]any) { m["iss"] = "https://keycloak.example.invalid/realms/other" }), "", 401, "unauthenticated", 1},
		{"wrong client", keycloakToken(func(m map[string]any) { m["client_id"] = "other-app" }), "", 401, "unauthenticated", 1},
		{"audience array", keycloakToken(func(m map[string]any) { m["client_id"] = "other-app"; m["aud"] = []any{"account", "birge-admin"} }), "", 200, "mfa=true", 1},
		{"refresh token", keycloakToken(func(m map[string]any) { m["typ"] = "Refresh" }), "", 401, "unauthenticated", 1},
		{"single role string", keycloakToken(func(m map[string]any) { m["realm_access"] = map[string]any{"roles": "birge-psychologist"} }), "", 200, "roles=psychologist mfa", 1},
		{"identity map value unknown", keycloakToken(func(m map[string]any) { m["realm_access"] = map[string]any{"roles": []any{"admin"}} }), "", 200, "roles= mfa", 1},
	})
}

func TestCache(t *testing.T) {
	const token = "tok-cache-0123456789"
	h := newHarness(t, hydraConfig(), map[string]map[string]any{
		token: hydraToken(func(m map[string]any) { m["exp"] = start.Add(90 * time.Second).Unix() }),
	})
	for range 3 {
		if rec := h.do(t, "Bearer "+token); rec.Code != 200 {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	if got := h.idp.calls.Load(); got != 1 {
		t.Fatalf("introspection calls = %d, want 1 (cached)", got)
	}
	// After the TTL the token is introspected again.
	h.clock.advance(61 * time.Second)
	if rec := h.do(t, "Bearer "+token); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := h.idp.calls.Load(); got != 2 {
		t.Fatalf("introspection calls = %d, want 2 after TTL", got)
	}
	// The cache never outlives exp (90s): at 91s the token is rejected.
	h.clock.advance(30 * time.Second)
	if rec := h.do(t, "Bearer "+token); rec.Code != 401 {
		t.Fatalf("after exp: status = %d, want 401", rec.Code)
	}
	if got := h.idp.calls.Load(); got != 3 {
		t.Fatalf("introspection calls = %d, want 3 (expired entry not served)", got)
	}
	// Rejected tokens are not cached.
	h.do(t, "Bearer tok-unknown")
	h.do(t, "Bearer tok-unknown")
	if got := h.idp.calls.Load(); got != 5 {
		t.Fatalf("introspection calls = %d, want 5", got)
	}
}

// A token without MFA stays refused even when served from the cache.
func TestCachedWithoutMFAStillForbidden(t *testing.T) {
	const token = "tok-nomfa-0123456789"
	h := newHarness(t, hydraConfig(), map[string]map[string]any{
		token: hydraToken(func(m map[string]any) { m["ext"] = map[string]any{"roles": []any{"admin"}} }),
	})
	for range 2 {
		if rec := h.do(t, "Bearer "+token); rec.Code != 403 {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	}
}

func TestIntrospectionDown(t *testing.T) {
	const token = "tok-down-01234567890"
	h := newHarness(t, hydraConfig(), map[string]map[string]any{token: hydraToken(nil)})
	h.idp.srv.Close()
	rec := h.do(t, "Bearer "+token)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "auth_unavailable") {
		t.Fatalf("status = %d body %s, want 503 auth_unavailable", rec.Code, rec.Body)
	}
	if strings.Contains(h.logs.String(), token) {
		t.Fatal("token logged")
	}
}

func TestIntrospectionFailures(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"server error", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }},
		{"client credentials refused", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }},
		{"not json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }},
		{"null", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("null")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			cfg := hydraConfig()
			cfg.IntrospectionURL, cfg.ClientID, cfg.ClientSecret = srv.URL, clientID, clientSecret
			cfg.Logger = slog.New(slog.DiscardHandler)
			mw, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/staff/escalations", nil)
			req.Header.Set("Authorization", "Bearer tok-0123456789")
			rec := httptest.NewRecorder()
			mw.Wrap(http.NotFoundHandler()).ServeHTTP(rec, req)
			if rec.Code != 503 {
				t.Fatalf("status = %d, want 503", rec.Code)
			}
		})
	}
}

// A redirect from the provider must not carry the token to another host.
func TestNoRedirectFollow(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("token") != "" || r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
	}))
	defer other.Close()
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/introspect", http.StatusTemporaryRedirect)
	}))
	defer idp.Close()
	cfg := hydraConfig()
	cfg.IntrospectionURL, cfg.ClientID, cfg.ClientSecret = idp.URL, clientID, clientSecret
	cfg.Client = &http.Client{}
	cfg.Logger = slog.New(slog.DiscardHandler)
	mw, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/staff/escalations", nil)
	req.Header.Set("Authorization", "Bearer tok-0123456789")
	rec := httptest.NewRecorder()
	mw.Wrap(http.NotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if leaked.Load() {
		t.Fatal("token forwarded on redirect")
	}
}

func TestUnconfigured(t *testing.T) {
	rec := httptest.NewRecorder()
	Unconfigured().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/v1/staff/escalations", nil))
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "staff_auth_not_configured") {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	valid := func() Config {
		c := hydraConfig()
		c.IntrospectionURL, c.ClientID, c.ClientSecret = "https://hydra.example.invalid/admin/oauth2/introspect", clientID, clientSecret
		return c
	}
	if _, err := New(valid()); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	tests := map[string]func(*Config){
		"relative url":       func(c *Config) { c.IntrospectionURL = "introspect" },
		"url credentials":    func(c *Config) { c.IntrospectionURL = "https://u:p@hydra.example.invalid/introspect" },
		"no client id":       func(c *Config) { c.ClientID = "" },
		"no secret":          func(c *Config) { c.ClientSecret = "" },
		"no issuer":          func(c *Config) { c.Issuer = "" },
		"no audience":        func(c *Config) { c.Audience = "" },
		"no mfa rule":        func(c *Config) { c.MFAACR, c.MFAAMR = nil, nil },
		"bad mfa value":      func(c *Config) { c.MFAACR = []string{`a"b`} },
		"bad claim path":     func(c *Config) { c.RolesClaim = "realm_access..roles" },
		"unknown role":       func(c *Config) { c.RoleMap = map[string]string{"x": "root"} },
		"empty idp value":    func(c *Config) { c.RoleMap = map[string]string{"": RoleAdmin} },
		"role map to lawyer": nil,
	}
	for name, mod := range tests {
		t.Run(name, func(t *testing.T) {
			c := valid()
			if mod == nil {
				c.RoleMap = map[string]string{"legal": RoleLawyer}
				if _, err := New(c); err != nil {
					t.Fatalf("lawyer mapping refused: %v", err)
				}
				return
			}
			mod(&c)
			if _, err := New(c); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestStaffID(t *testing.T) {
	if got := StaffID(keycloakIssuer, alice); got.String() != alice {
		t.Errorf("UUID subject = %s, want it unchanged", got)
	}
	a1, a2 := StaffID(hydraIssuer, "staff:alice"), StaffID(hydraIssuer, "staff:alice")
	if a1 != a2 || a1.Version() != 5 {
		t.Errorf("derived ID not stable or not v5: %s %s", a1, a2)
	}
	if StaffID(keycloakIssuer, "staff:alice") == a1 {
		t.Error("equal subjects of different issuers collide")
	}
	if StaffID(hydraIssuer, "{"+alice+"}") == uuid.MustParse(alice) {
		t.Error("non-canonical UUID form used as is")
	}
	if StaffID(hydraIssuer, uuid.Nil.String()) == uuid.Nil {
		t.Error("nil UUID subject accepted as is")
	}
	// Pinned: changing the namespace or the derivation would re-identify
	// every staff member with a non-UUID subject.
	if got := StaffID("https://idp.example.invalid/", "user-1").String(); got != pinnedDerivedID {
		t.Errorf("derived ID = %s, want %s", got, pinnedDerivedID)
	}
}

const pinnedDerivedID = "27d32129-0833-5e9a-8395-1c4dfb06b136"
