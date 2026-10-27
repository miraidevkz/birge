package wiring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/platform/crypto"
	platformdb "birge/internal/platform/db"
	"birge/internal/platform/ratelimit"
	"birge/internal/platform/testinfra"
)

const (
	e2eToken       = "tok-e2e-valid-00000000"
	e2eDisplayName = "Example Name"
)

// syncBuffer is a log sink safe for the concurrent writes of an HTTP stack.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeKratos answers whoami for e2eToken (identity) and the admin identity
// lookup for the same identity.
func fakeKratos(t *testing.T, identity uuid.UUID) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions/whoami", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Session-Token") != e2eToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"active":true,"expires_at":%q,"authenticator_assurance_level":"aal1","identity":{"id":%q}}`,
			time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), identity)
	})
	mux.HandleFunc("GET /admin/identities/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != identity.String() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"traits":{"phone":%q}}`, identity, testPhone)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// migratedDatabase creates an empty database in a throwaway cluster and
// brings it up with the embedded migrations (twice, to prove idempotency)
// and db/roles.sql. testinfra.Postgres applies db/schema directly, so the
// test uses a second database of the same cluster.
func migratedDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	admin := testinfra.Postgres(t)
	for _, stmt := range []string{"CREATE DATABASE birge_e2e", "CREATE ROLE birge_app NOLOGIN"} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	cc := admin.Config().ConnConfig
	url := fmt.Sprintf("host=%s port=%d user=%s dbname=birge_e2e sslmode=disable", cc.Host, cc.Port, cc.User)
	logger := slog.New(slog.DiscardHandler)
	for range 2 {
		if err := Migrate(ctx, logger, Config{DatabaseURL: url, ApplyRoles: true}); err != nil {
			t.Fatal(err)
		}
	}
	return url
}

type e2e struct {
	t        *testing.T
	pool     *pgxpool.Pool
	envelope *crypto.Envelope
	cfg      Config
	logs     *syncBuffer
	identity uuid.UUID
	env      map[string]string // the environment cfg was loaded from
	files    fakeFiles
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	ctx := context.Background()
	url := migratedDatabase(t)
	pool, err := platformdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	identity := uuid.New()
	kratos := fakeKratos(t, identity)
	env := validEnv(t)
	env["DATABASE_URL"] = url
	env["KRATOS_PUBLIC_URL"] = kratos.URL
	env["KRATOS_ADMIN_URL"] = kratos.URL
	env["BIRGE_MIN_APP_VERSION"] = "1.0.0"
	files := validFiles(t)
	cfg, err := load(env, files, CommandAPI)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := NewEnvelope(pool, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &e2e{t: t, pool: pool, envelope: envelope, cfg: cfg, logs: &syncBuffer{}, identity: identity, env: env, files: files}
}

func (e *e2e) handler(cfg Config) http.Handler {
	e.t.Helper()
	h, err := NewAPIHandler(APIDeps{
		Config: cfg, Pool: e.pool, Envelope: e.envelope,
		Logger: slog.New(slog.NewJSONHandler(e.logs, nil)),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return h
}

func do(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, r)
	req.Header.Set("X-App-Version", "1.2.0")
	req.Header.Set("X-App-Platform", "android")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPIEndToEnd(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	h := e.handler(e.cfg)

	// Anonymous operation.
	rec := do(t, h, http.MethodGet, "/v1/meta/client-config", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"minSupportedVersion":"1.0.0"`) {
		t.Fatalf("client-config: %d %s", rec.Code, rec.Body)
	}
	// Every other operation needs a session.
	if rec := do(t, h, http.MethodGet, "/v1/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/me without token: %d, want 401", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/v1/me", "tok-e2e-unknown-000000", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/me with unknown token: %d, want 401", rec.Code)
	}

	// The first authenticated request creates the member.
	rec = do(t, h, http.MethodGet, "/v1/me", e2eToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me: %d %s", rec.Code, rec.Body)
	}
	var me struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.ID == uuid.Nil {
		t.Fatalf("GET /v1/me body %s: %v", rec.Body, err)
	}
	var stored uuid.UUID
	if err := e.pool.QueryRow(ctx, "SELECT id FROM membership.members WHERE identity_id = $1", e.identity).Scan(&stored); err != nil || stored != me.ID {
		t.Fatalf("member row = %s, %v; want %s", stored, err, me.ID)
	}

	// The display name is sealed at rest with the member's key, bound to its column and row.
	rec = do(t, h, http.MethodPatch, "/v1/me", e2eToken, `{"displayName":"`+e2eDisplayName+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), e2eDisplayName) {
		t.Fatalf("PATCH /v1/me: %d %s", rec.Code, rec.Body)
	}
	var sealed []byte
	if err := e.pool.QueryRow(ctx, "SELECT display_name FROM membership.members WHERE id = $1", me.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if len(sealed) == 0 || bytes.Contains(sealed, []byte(e2eDisplayName)) {
		t.Fatalf("display_name stored in plaintext or empty (%d bytes)", len(sealed))
	}
	plain, err := e.envelope.Open(ctx, me.ID, sealed, crypto.AssociatedData(DisplayNameField, me.ID))
	if err != nil || string(plain) != e2eDisplayName {
		t.Fatalf("open stored name: %q, %v", plain, err)
	}

	// Privacy keys its rows by the member ID, not the Kratos identity, so the
	// deletion timer shreds the member's real key.
	rec = do(t, h, http.MethodPost, "/v1/me/deletion-request", e2eToken, `{"confirm":"DELETE"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/me/deletion-request: %d %s", rec.Code, rec.Body)
	}
	var timerSubject uuid.UUID
	if err := e.pool.QueryRow(ctx, "SELECT subject_id FROM platform.timers WHERE kind = 'privacy.DeletionDue'").Scan(&timerSubject); err != nil || timerSubject != me.ID {
		t.Fatalf("deletion timer subject = %s, %v; want member %s", timerSubject, err, me.ID)
	}
	if rec := do(t, h, http.MethodGet, "/v1/me/inbox", e2eToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me/inbox: %d %s", rec.Code, rec.Body)
	}

	// Health endpoints sit outside /v1 and need no session.
	if rec := do(t, h, http.MethodGet, "/readyz", "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("/readyz: %d", rec.Code)
	}

	logs := e.logs.String()
	for _, secret := range []string{e2eDisplayName, e2eToken, "192.0.2.1"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain %q", secret)
		}
	}
	if !strings.Contains(logs, `"route":"PATCH /v1/me"`) {
		t.Errorf("request log lacks the route pattern: %s", logs)
	}

	// Staff authentication is not built yet: a member session must not reach
	// the escalation queue (session never sets Staff=true → 403).
	if rec := do(t, h, http.MethodGet, "/v1/staff/escalations", e2eToken, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("GET /v1/staff/escalations with member token: %d %s, want 403", rec.Code, rec.Body)
	}
}

func TestAPIRateLimit(t *testing.T) {
	e := newE2E(t)
	cfg := e.cfg
	cfg.APIPerIP = ratelimit.Rate{Burst: 2, Every: time.Hour}
	h := e.handler(cfg)
	for i := range 2 {
		if rec := do(t, h, http.MethodGet, "/v1/meta/client-config", "", ""); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	rec := do(t, h, http.MethodGet, "/v1/meta/client-config", "", "")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("third request: %d (Retry-After %q), want 429", rec.Code, rec.Header().Get("Retry-After"))
	}
	// Health checks are not rate limited.
	if rec := do(t, h, http.MethodGet, "/healthz", "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("/healthz: %d", rec.Code)
	}
}
