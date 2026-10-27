// Command birge-devlogin is the login and consent app of the development
// Ory Hydra in deploy/compose. It exists so the staff admin can be tried
// without a real identity provider: the sign-in page lists one fixed account
// per staff role and signs in whichever is picked, without a password.
//
// Development only. It authenticates nobody: anyone who reaches it becomes
// any staff role. The compose stack publishes it on 127.0.0.1 only, and the
// tokens it produces carry amr "dev" instead of a real second factor, which
// only the development API configuration (BIRGE_STAFF_MFA_AMR=dev) accepts.
// A production deployment uses Hydra with Kratos (WebAuthn) or another OIDC
// provider (ADR-0008) and never runs this command.
//
// Flow (Hydra login and consent API): Hydra redirects the browser to
// /login?login_challenge=…; the picked account is accepted with its subject
// and role kept in the login context; Hydra then redirects to
// /consent?consent_challenge=…, which is accepted at once with the requested
// scopes and audience, putting {"roles": [role], "amr": ["dev"]} into the
// access-token session. Hydra's introspection returns those under "ext",
// where staffauth reads roles (BIRGE_STAFF_ROLES_CLAIM=ext.roles) and amr.
//
// Configuration: HYDRA_ADMIN_URL (Hydra's admin API, e.g. http://hydra:4445)
// and LISTEN (default :3000).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// account is one fixed development staff member.
type account struct {
	Role    string
	Subject string // a UUID: safety stores staff IDs as UUIDs
	Label   string
}

// accounts are fixed so the same role keeps the same staff ID across
// restarts (duty shifts and audit entries refer to it).
var accounts = []account{
	{"psychologist", "00000000-0000-4000-8000-00000000d001", "Psychologist"},
	{"moderator", "00000000-0000-4000-8000-00000000d002", "Moderator"},
	{"lawyer", "00000000-0000-4000-8000-00000000d003", "Lawyer"},
	{"admin", "00000000-0000-4000-8000-00000000d004", "Admin"},
}

func accountFor(role string) (account, bool) {
	for _, a := range accounts {
		if a.Role == role {
			return a, true
		}
	}
	return account{}, false
}

var page = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Birge staff sign-in (development)</title>
<style>body{font:16px system-ui,sans-serif;max-width:28rem;margin:3rem auto;padding:0 1rem}
button{display:block;width:100%;margin:.5rem 0;padding:.8rem;font:inherit;cursor:pointer}
p.warn{background:#fff3cd;color:#664d03;padding:.6rem;border-radius:.4rem}</style></head>
<body><h1>Staff sign-in</h1>
<p class="warn">Development identity provider: no password, no second factor. Never deploy it.</p>
<form method="post" action="/login"><input type="hidden" name="challenge" value="{{.Challenge}}">
{{range .Accounts}}<button name="role" value="{{.Role}}">{{.Label}}</button>{{end}}
</form></body></html>`))

type server struct {
	admin  string
	client *http.Client
	log    *slog.Logger
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	admin := strings.TrimRight(strings.TrimSpace(os.Getenv("HYDRA_ADMIN_URL")), "/")
	if u, err := url.Parse(admin); admin == "" || err != nil || u.Host == "" {
		log.Error("HYDRA_ADMIN_URL must be the absolute URL of Hydra's admin API")
		os.Exit(2)
	}
	addr := os.Getenv("LISTEN")
	if addr == "" {
		addr = ":3000"
	}
	s := &server{admin: admin, client: &http.Client{Timeout: 10 * time.Second}, log: log}
	srv := &http.Server{Addr: addr, Handler: newMux(s), ReadHeaderTimeout: 5 * time.Second}
	log.Warn("development login app started: it signs anyone in as any staff role", "event", "DevLoginStarted", "addr", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// newMux registers the login, consent and health handlers on a ServeMux.
func newMux(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginAccept)
	mux.HandleFunc("GET /consent", s.consent)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return mux
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("login_challenge")
	if challenge == "" {
		http.Error(w, "missing login_challenge: open the admin app and sign in from there", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := page.Execute(w, map[string]any{"Challenge": challenge, "Accounts": accounts}); err != nil {
		s.log.Error("render login page", "error", err)
	}
}

func (s *server) loginAccept(w http.ResponseWriter, r *http.Request) {
	challenge := r.PostFormValue("challenge")
	a, ok := accountFor(r.PostFormValue("role"))
	if challenge == "" || !ok {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body := map[string]any{
		"subject":  a.Subject,
		"remember": false,
		"amr":      []string{"dev"},
		// The consent step reads the role back from the login context.
		"context": map[string]string{"role": a.Role},
	}
	s.accept(w, r, "login", challenge, body)
}

func (s *server) consent(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("consent_challenge")
	if challenge == "" {
		http.Error(w, "missing consent_challenge", http.StatusBadRequest)
		return
	}
	var req struct {
		RequestedScope    []string          `json:"requested_scope"`
		RequestedAudience []string          `json:"requested_access_token_audience"`
		Context           map[string]string `json:"context"`
	}
	if err := s.call(r.Context(), http.MethodGet, "consent", challenge, "", nil, &req); err != nil {
		s.fail(w, "fetch consent request", err)
		return
	}
	a, ok := accountFor(req.Context["role"])
	if !ok {
		http.Error(w, "unknown role in login context", http.StatusBadRequest)
		return
	}
	claims := map[string]any{"roles": []string{a.Role}, "amr": []string{"dev"}}
	body := map[string]any{
		"grant_scope":                 req.RequestedScope,
		"grant_access_token_audience": req.RequestedAudience,
		"remember":                    false,
		"session":                     map[string]any{"access_token": claims, "id_token": claims},
	}
	s.accept(w, r, "consent", challenge, body)
}

// accept accepts a login or consent request and sends the browser on.
func (s *server) accept(w http.ResponseWriter, r *http.Request, kind, challenge string, body any) {
	var out struct {
		RedirectTo string `json:"redirect_to"`
	}
	if err := s.call(r.Context(), http.MethodPut, kind, challenge, "/accept", body, &out); err != nil {
		s.fail(w, "accept "+kind, err)
		return
	}
	http.Redirect(w, r, out.RedirectTo, http.StatusFound)
}

// call sends one request to Hydra's admin API for a login or consent challenge.
func (s *server) call(ctx context.Context, method, kind, challenge, suffix string, in, out any) error {
	u := fmt.Sprintf("%s/admin/oauth2/auth/requests/%s%s?%s_challenge=%s",
		s.admin, kind, suffix, kind, url.QueryEscape(challenge))
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", kind, err)
		}
		rd = bytes.NewReader(b)
	}
	// u is the configured admin URL plus a fixed path and the escaped challenge.
	req, err := http.NewRequestWithContext(ctx, method, u, rd) //nolint:gosec // G704: host comes from HYDRA_ADMIN_URL only
	if err != nil {
		return fmt.Errorf("build %s request: %w", kind, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req) //nolint:gosec // G704: see above
	if err != nil {
		return fmt.Errorf("hydra %s: %w", kind, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("hydra: %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return errors.Join(errors.New("hydra: bad response"), err)
	}
	return nil
}

func (s *server) fail(w http.ResponseWriter, what string, err error) {
	s.log.Error(what, "error", err)
	http.Error(w, "identity provider error", http.StatusBadGateway)
}
