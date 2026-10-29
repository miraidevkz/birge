package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// recorded is one request the fake Hydra admin API received.
type recorded struct {
	Method string
	Path   string
	Query  url.Values
	Body   map[string]any
}

// fakeHydra is an httptest.Server that records admin API calls and returns
// scripted responses for login/consent get and accept.
type fakeHydra struct {
	server   *httptest.Server
	recorded []recorded

	// loginAcceptStatus is the HTTP status for PUT .../login/accept (0 = 200).
	loginAcceptStatus int
	// loginAcceptBody is the raw response body for login accept; empty uses a redirect JSON.
	loginAcceptBody string
	// consentGetStatus is the HTTP status for GET .../consent (0 = 200).
	consentGetStatus int
	// consentGetBody is the raw response for consent get; empty uses a default request.
	consentGetBody string
	// consentAcceptStatus is the HTTP status for PUT .../consent/accept (0 = 200).
	consentAcceptStatus int
	// consentAcceptBody is the raw response for consent accept; empty uses a redirect JSON.
	consentAcceptBody string
}

func newFakeHydra(t *testing.T) *fakeHydra {
	t.Helper()
	f := &fakeHydra{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recorded{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query()}
		if r.Body != nil {
			b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			if len(b) > 0 {
				if err := json.Unmarshal(b, &rec.Body); err != nil {
					t.Errorf("decode body: %v", err)
				}
			}
		}
		f.recorded = append(f.recorded, rec)

		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/login/accept"):
			status := f.loginAcceptStatus
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			body := f.loginAcceptBody
			if body == "" && status/100 == 2 {
				body = `{"redirect_to":"https://hydra.example.invalid/login-done"}`
			}
			_, _ = w.Write([]byte(body))

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/consent") && !strings.HasSuffix(r.URL.Path, "/accept"):
			status := f.consentGetStatus
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			body := f.consentGetBody
			if body == "" && status/100 == 2 {
				body = `{
					"requested_scope":["openid","offline"],
					"requested_access_token_audience":["staff-api"],
					"context":{"role":"psychologist"}
				}`
			}
			_, _ = w.Write([]byte(body))

		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/consent/accept"):
			status := f.consentAcceptStatus
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			body := f.consentAcceptBody
			if body == "" && status/100 == 2 {
				body = `{"redirect_to":"https://hydra.example.invalid/consent-done"}`
			}
			_, _ = w.Write([]byte(body))

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"unexpected"}`))
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func testServer(t *testing.T, admin string) http.Handler {
	t.Helper()
	return newMux(&server{
		admin:  strings.TrimRight(admin, "/"),
		client: &http.Client{},
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestLoginPage(t *testing.T) {
	h := testServer(t, "http://hydra.example.invalid")

	tests := []struct {
		name       string
		challenge  string
		wantStatus int
		wantRoles  []string
		wantEscape string // substring that must appear escaped in HTML
	}{
		{
			name:       "missing challenge",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "lists every role and challenge",
			challenge:  "chal-abc",
			wantStatus: http.StatusOK,
			wantRoles:  []string{"psychologist", "moderator", "lawyer", "admin"},
		},
		{
			name:       "html-escapes challenge",
			challenge:  `"><script>x</script>`,
			wantStatus: http.StatusOK,
			wantRoles:  []string{"psychologist", "moderator", "lawyer", "admin"},
			wantEscape: "&#34;&gt;&lt;script&gt;x&lt;/script&gt;",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := "/login"
			if tt.challenge != "" {
				u += "?login_challenge=" + url.QueryEscape(tt.challenge)
			}
			req := httptest.NewRequest(http.MethodGet, u, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			body := rec.Body.String()
			for _, role := range tt.wantRoles {
				if !strings.Contains(body, `value="`+role+`"`) {
					t.Errorf("HTML missing role button %q", role)
				}
			}
			if tt.wantEscape != "" {
				if !strings.Contains(body, tt.wantEscape) {
					t.Errorf("HTML missing escaped challenge %q in %q", tt.wantEscape, body)
				}
				if strings.Contains(body, tt.challenge) {
					t.Errorf("HTML contains raw challenge %q", tt.challenge)
				}
			} else if !strings.Contains(body, `value="`+tt.challenge+`"`) {
				t.Errorf("HTML missing challenge %q", tt.challenge)
			}
		})
	}
}

func TestLoginAcceptBadRequest(t *testing.T) {
	hydra := newFakeHydra(t)
	h := testServer(t, hydra.server.URL)

	tests := []struct {
		name string
		form url.Values
	}{
		{"unknown role", url.Values{"challenge": {"c1"}, "role": {"nobody"}}},
		{"no challenge", url.Values{"role": {"psychologist"}}},
		{"empty form", url.Values{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hydra.recorded = nil
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if len(hydra.recorded) != 0 {
				t.Fatalf("Hydra called %d times, want 0: %+v", len(hydra.recorded), hydra.recorded)
			}
		})
	}
}

func TestLoginAcceptPsychologist(t *testing.T) {
	hydra := newFakeHydra(t)
	h := testServer(t, hydra.server.URL)

	a, ok := accountFor("psychologist")
	if !ok {
		t.Fatal("psychologist account missing")
	}
	challenge := "login-chal/with spaces&special=1"
	form := url.Values{"challenge": {challenge}, "role": {"psychologist"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body %q", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "https://hydra.example.invalid/login-done" {
		t.Errorf("Location = %q", loc)
	}
	if len(hydra.recorded) != 1 {
		t.Fatalf("Hydra calls = %d, want 1: %+v", len(hydra.recorded), hydra.recorded)
	}
	got := hydra.recorded[0]
	if got.Method != http.MethodPut {
		t.Errorf("method = %s, want PUT", got.Method)
	}
	if !strings.HasSuffix(got.Path, "/requests/login/accept") {
		t.Errorf("path = %q, want .../requests/login/accept", got.Path)
	}
	if got.Query.Get("login_challenge") != challenge {
		t.Errorf("login_challenge = %q, want %q", got.Query.Get("login_challenge"), challenge)
	}
	if got.Body["subject"] != a.Subject {
		t.Errorf("subject = %v, want %s", got.Body["subject"], a.Subject)
	}
	amr, _ := got.Body["amr"].([]any)
	if len(amr) != 1 || amr[0] != "dev" {
		t.Errorf("amr = %v, want [dev]", got.Body["amr"])
	}
	ctx, _ := got.Body["context"].(map[string]any)
	if ctx["role"] != "psychologist" {
		t.Errorf("context.role = %v, want psychologist", ctx["role"])
	}
}

func TestConsent(t *testing.T) {
	hydra := newFakeHydra(t)
	h := testServer(t, hydra.server.URL)

	req := httptest.NewRequest(http.MethodGet, "/consent?consent_challenge="+url.QueryEscape("consent-1"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body %q", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "https://hydra.example.invalid/consent-done" {
		t.Errorf("Location = %q", loc)
	}
	if len(hydra.recorded) != 2 {
		t.Fatalf("Hydra calls = %d, want 2: %+v", len(hydra.recorded), hydra.recorded)
	}
	get := hydra.recorded[0]
	if get.Method != http.MethodGet {
		t.Errorf("first method = %s, want GET", get.Method)
	}
	if !strings.Contains(get.Path, "/requests/consent") || strings.HasSuffix(get.Path, "/accept") {
		t.Errorf("first path = %q, want GET .../requests/consent", get.Path)
	}
	if get.Query.Get("consent_challenge") != "consent-1" {
		t.Errorf("consent_challenge = %q", get.Query.Get("consent_challenge"))
	}

	put := hydra.recorded[1]
	if put.Method != http.MethodPut {
		t.Errorf("second method = %s, want PUT", put.Method)
	}
	if !strings.HasSuffix(put.Path, "/requests/consent/accept") {
		t.Errorf("second path = %q", put.Path)
	}
	if put.Query.Get("consent_challenge") != "consent-1" {
		t.Errorf("accept consent_challenge = %q", put.Query.Get("consent_challenge"))
	}
	scope, _ := put.Body["grant_scope"].([]any)
	if len(scope) != 2 || scope[0] != "openid" || scope[1] != "offline" {
		t.Errorf("grant_scope = %v", put.Body["grant_scope"])
	}
	aud, _ := put.Body["grant_access_token_audience"].([]any)
	if len(aud) != 1 || aud[0] != "staff-api" {
		t.Errorf("grant_access_token_audience = %v", put.Body["grant_access_token_audience"])
	}
	session, _ := put.Body["session"].(map[string]any)
	at, _ := session["access_token"].(map[string]any)
	roles, _ := at["roles"].([]any)
	amr, _ := at["amr"].([]any)
	if len(roles) != 1 || roles[0] != "psychologist" {
		t.Errorf("session.access_token.roles = %v", at["roles"])
	}
	if len(amr) != 1 || amr[0] != "dev" {
		t.Errorf("session.access_token.amr = %v", at["amr"])
	}
	if at["name"] != "Psychologist (demo)" {
		t.Errorf("session.access_token.name = %v", at["name"])
	}
}

func TestHydraErrors(t *testing.T) {
	tests := []struct {
		name string
		path string // request path on our app
		prep func(*fakeHydra)
	}{
		{
			name: "login accept 500",
			path: "/login",
			prep: func(f *fakeHydra) { f.loginAcceptStatus = http.StatusInternalServerError },
		},
		{
			name: "login accept invalid JSON",
			path: "/login",
			prep: func(f *fakeHydra) { f.loginAcceptBody = "not-json" },
		},
		{
			name: "consent get 500",
			path: "/consent?consent_challenge=c",
			prep: func(f *fakeHydra) { f.consentGetStatus = http.StatusInternalServerError },
		},
		{
			name: "consent get invalid JSON",
			path: "/consent?consent_challenge=c",
			prep: func(f *fakeHydra) { f.consentGetBody = "{" },
		},
		{
			name: "consent accept 500",
			path: "/consent?consent_challenge=c",
			prep: func(f *fakeHydra) { f.consentAcceptStatus = http.StatusInternalServerError },
		},
		{
			name: "consent accept invalid JSON",
			path: "/consent?consent_challenge=c",
			prep: func(f *fakeHydra) { f.consentAcceptBody = "nope" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hydra := newFakeHydra(t)
			tt.prep(hydra)
			h := testServer(t, hydra.server.URL)

			var req *http.Request
			if strings.HasPrefix(tt.path, "/login") {
				form := url.Values{"challenge": {"c"}, "role": {"psychologist"}}
				req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			} else {
				req = httptest.NewRequest(http.MethodGet, tt.path, nil)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body %q", rec.Code, rec.Body.String())
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("unexpected redirect to %q", loc)
			}
		})
	}
}

func TestAccountsSubjects(t *testing.T) {
	seen := make(map[string]string)
	for _, a := range accounts {
		if !validUUID(a.Subject) {
			t.Errorf("role %q subject %q is not a UUID", a.Role, a.Subject)
		}
		if other, ok := seen[a.Subject]; ok {
			t.Errorf("subject %q shared by %q and %q", a.Subject, other, a.Role)
		}
		seen[a.Subject] = a.Role
	}
}

// validUUID reports whether s is an 8-4-4-4-12 hex UUID (staffauth needs UUIDs).
func validUUID(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		return false
	}
	for _, p := range parts {
		for _, c := range p {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}
