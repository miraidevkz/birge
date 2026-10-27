// Package session authenticates API requests with the Kratos session token
// (ADR-0008, addendum 2026-10-27).
//
// The middleware reads "Authorization: Bearer <token>", validates the token
// against Kratos "GET /sessions/whoami" (header X-Session-Token) and stores an
// auth.Principal in the request context. Valid sessions are cached for at most
// CacheTTL (default 60s) in a bounded LRU keyed by the SHA-256 of the token;
// the raw token is never stored, logged or sent anywhere except Kratos.
// Consequence of the cache: a revoked session stays usable for up to CacheTTL
// on each replica.
//
// Invalid or missing credentials get 401; an unreachable or failing Kratos
// gets 503. Routes declared with "security: []" in the contract are listed in
// Config.Anonymous and pass without a principal.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"birge/internal/platform/auth"
	"birge/internal/platform/bearer"
	"birge/internal/platform/clock"
	"birge/internal/platform/httpapi"
)

const (
	defaultCacheTTL  = 60 * time.Second
	defaultCacheSize = 10_000
	defaultTimeout   = 5 * time.Second
	maxTokenLen      = 512
	maxWhoamiBytes   = 64 << 10
)

// Config configures the middleware.
type Config struct {
	// KratosPublicURL is the base URL of the Kratos public API (cluster-internal).
	KratosPublicURL string
	// Client calls Kratos. Optional; redirects are always disabled so the
	// token cannot be forwarded to another host.
	Client *http.Client
	// CacheTTL bounds how long a validated session is trusted without asking
	// Kratos again. Default and maximum: 60s.
	CacheTTL time.Duration
	// CacheSize is the maximum number of cached sessions. Default 10000.
	CacheSize int
	// Anonymous lists http.ServeMux patterns ("GET /v1/meta/client-config",
	// "GET /v1/reference/") of routes without security; they pass without a principal.
	Anonymous []string
	Clock     clock.Clock
	Logger    *slog.Logger
}

// Middleware validates session tokens.
type Middleware struct {
	whoami *url.URL
	client *http.Client
	ttl    time.Duration
	cache  *bearer.Cache[auth.Principal]
	anon   *http.ServeMux
	clock  clock.Clock
	logger *slog.Logger
}

// anonMarker marks anonymous routes in the pattern matcher; only a match on
// this exact handler type counts (not a redirect the mux may return).
type anonMarker struct{}

func (anonMarker) ServeHTTP(http.ResponseWriter, *http.Request) {}

// New validates cfg and returns the middleware.
func New(cfg Config) (*Middleware, error) {
	base, err := url.Parse(cfg.KratosPublicURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("session: KratosPublicURL must be an absolute http(s) URL")
	}
	m := &Middleware{
		whoami: base.JoinPath("sessions", "whoami"),
		ttl:    cfg.CacheTTL,
		clock:  cfg.Clock,
		logger: cfg.Logger,
		anon:   http.NewServeMux(),
	}
	if m.ttl <= 0 || m.ttl > defaultCacheTTL {
		m.ttl = defaultCacheTTL
	}
	size := cfg.CacheSize
	if size <= 0 {
		size = defaultCacheSize
	}
	m.cache = bearer.NewCache[auth.Principal](size)
	if m.clock == nil {
		m.clock = clock.System{}
	}
	if m.logger == nil {
		m.logger = slog.Default()
	}
	client := &http.Client{Timeout: defaultTimeout}
	if cfg.Client != nil {
		c := *cfg.Client
		client = &c
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	m.client = client
	for _, p := range cfg.Anonymous {
		if err := registerAnon(m.anon, p); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func registerAnon(mux *http.ServeMux, pattern string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("session: invalid anonymous pattern %q: %v", pattern, r)
		}
	}()
	mux.Handle(pattern, anonMarker{})
	return nil
}

// Wrap returns next behind session authentication.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.isAnonymous(r) {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := bearer.Token(r, maxTokenLen)
		if !ok {
			unauthorized(w)
			return
		}
		p, err := m.authenticate(r.Context(), token)
		switch {
		case errors.Is(err, errInvalidSession):
			unauthorized(w)
			return
		case err != nil:
			m.logger.Warn("session validation unavailable", "error", err)
			httpapi.WriteProblem(w, http.StatusServiceUnavailable, "auth_unavailable", "Sign-in is temporarily unavailable.")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

func (m *Middleware) isAnonymous(r *http.Request) bool {
	h, _ := m.anon.Handler(r)
	_, ok := h.(anonMarker)
	return ok
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="birge"`)
	httpapi.WriteProblem(w, http.StatusUnauthorized, "unauthenticated", "Sign in again.")
}

var errInvalidSession = errors.New("invalid session")

// whoamiResponse is the subset of the Kratos session we use.
type whoamiResponse struct {
	Active    bool      `json:"active"`
	ExpiresAt time.Time `json:"expires_at"`
	AAL       string    `json:"authenticator_assurance_level"`
	Identity  struct {
		ID string `json:"id"`
	} `json:"identity"`
}

// Authenticate validates a raw session token (for the chat WebSocket's first
// frame). It returns a principal with IdentityID set; the caller resolves
// MemberID. Invalid tokens return an error that errors.Is can match with
// ErrInvalidSession.
func (m *Middleware) Authenticate(ctx context.Context, token string) (auth.Principal, error) {
	return m.authenticate(ctx, token)
}

// ErrInvalidSession is returned by Authenticate when the token is unknown,
// inactive or expired.
var ErrInvalidSession = errInvalidSession

func (m *Middleware) authenticate(ctx context.Context, token string) (auth.Principal, error) {
	now := m.clock.Now()
	if p, ok := m.cache.Get(token, now); ok {
		return p, nil
	}
	s, err := m.callWhoami(ctx, token)
	if err != nil {
		return auth.Principal{}, err
	}
	if !s.Active || !now.Before(s.ExpiresAt) {
		return auth.Principal{}, errInvalidSession
	}
	id, err := uuid.Parse(s.Identity.ID)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("kratos whoami: identity id: %w", err)
	}
	p := auth.Principal{IdentityID: id, MFA: s.AAL == "aal2"}
	expires := now.Add(m.ttl)
	if s.ExpiresAt.Before(expires) {
		expires = s.ExpiresAt
	}
	m.cache.Put(token, p, expires)
	return p, nil
}

func (m *Middleware) callWhoami(ctx context.Context, token string) (whoamiResponse, error) {
	var s whoamiResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.whoami.String(), nil)
	if err != nil {
		return s, fmt.Errorf("kratos whoami: %w", err)
	}
	req.Header.Set("X-Session-Token", token)
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return s, fmt.Errorf("kratos whoami: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := io.LimitReader(resp.Body, maxWhoamiBytes)
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		_, _ = io.Copy(io.Discard, body)
		return s, errInvalidSession
	default:
		_, _ = io.Copy(io.Discard, body)
		return s, fmt.Errorf("kratos whoami: status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(body).Decode(&s); err != nil {
		return s, fmt.Errorf("kratos whoami: decode: %w", err)
	}
	return s, nil
}
