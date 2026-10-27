package staffauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"birge/internal/platform/auth"
	"birge/internal/platform/bearer"
	"birge/internal/platform/clock"
	"birge/internal/platform/httpapi"
)

const (
	defaultCacheTTL  = 60 * time.Second
	defaultCacheSize = 1_000
	defaultTimeout   = 5 * time.Second
	// maxTokenLen admits JWT access tokens (Keycloak) with many claims;
	// the server's header limit (16 KiB) bounds it anyway.
	maxTokenLen          = 8 << 10
	maxIntrospectionBody = 64 << 10
	realm                = `Bearer realm="birge-staff"`
)

// Config configures the middleware. Every field without a default is
// required; New refuses an incomplete configuration rather than accept
// tokens on weaker terms.
type Config struct {
	// IntrospectionURL is the provider's RFC 7662 endpoint (Hydra:
	// <admin>/admin/oauth2/introspect; Keycloak:
	// <base>/realms/<realm>/protocol/openid-connect/token/introspect).
	IntrospectionURL string
	// ClientID and ClientSecret authenticate the backend to the endpoint.
	ClientID     string
	ClientSecret string
	// Issuer must equal the "iss" claim exactly.
	Issuer string
	// Audience must be one of "aud" or equal "client_id".
	Audience string
	// RolesClaim is the dotted claim path of the role values. Default "roles".
	RolesClaim string
	// RoleMap translates IdP role values into staff roles (Roles). Nil maps
	// each staff role name to itself (the IdP already uses our contract).
	RoleMap map[string]string
	// MFAACR are "acr" values that assert MFA; MFAAMR are "amr" values that
	// do. At least one value in either list is required.
	MFAACR []string
	MFAAMR []string
	// Client calls the endpoint. Optional; redirects are always disabled.
	Client *http.Client
	// CacheTTL bounds how long an introspection result is trusted. Default
	// and maximum: 60s.
	CacheTTL time.Duration
	// CacheSize is the maximum number of cached tokens. Default 1000.
	CacheSize int
	Clock     clock.Clock
	Logger    *slog.Logger
}

// Middleware validates staff access tokens.
type Middleware struct {
	endpoint     string
	clientID     string
	clientSecret string
	policy       policy
	stepUp       string
	client       *http.Client
	ttl          time.Duration
	cache        *bearer.Cache[auth.Principal]
	clock        clock.Clock
	logger       *slog.Logger
}

// New validates cfg and returns the middleware. Errors name the field,
// never a value.
func New(cfg Config) (*Middleware, error) {
	u, err := url.Parse(cfg.IntrospectionURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, errors.New("staffauth: IntrospectionURL must be an absolute http(s) URL without credentials")
	}
	switch {
	case cfg.ClientID == "" || cfg.ClientSecret == "":
		return nil, errors.New("staffauth: ClientID and ClientSecret are required")
	case cfg.Issuer == "":
		return nil, errors.New("staffauth: Issuer is required")
	case cfg.Audience == "":
		return nil, errors.New("staffauth: Audience is required")
	case len(cfg.MFAACR) == 0 && len(cfg.MFAAMR) == 0:
		return nil, errors.New("staffauth: MFAACR or MFAAMR is required (MFA is mandatory for staff)")
	}
	for _, v := range append(append([]string(nil), cfg.MFAACR...), cfg.MFAAMR...) {
		if !isClaimToken(v) {
			return nil, errors.New("staffauth: MFA values must be non-empty printable ASCII without spaces or quotes")
		}
	}
	rolesClaim := cfg.RolesClaim
	if rolesClaim == "" {
		rolesClaim = DefaultRolesClaim
	}
	for _, seg := range strings.Split(rolesClaim, ".") {
		if seg == "" {
			return nil, errors.New("staffauth: RolesClaim must be a dotted claim path such as realm_access.roles")
		}
	}
	roleMap := cfg.RoleMap
	if roleMap == nil {
		roleMap = make(map[string]string, len(Roles))
		for _, r := range Roles {
			roleMap[r] = r
		}
	}
	for k, r := range roleMap {
		if k == "" || !RoleIsValid(r) {
			return nil, errors.New("staffauth: RoleMap must map non-empty IdP values to " + strings.Join(Roles, ", "))
		}
	}
	m := &Middleware{
		endpoint:     u.String(),
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		policy: policy{
			issuer: cfg.Issuer, audience: cfg.Audience, rolesClaim: rolesClaim, roleMap: roleMap,
			mfaACR: append([]string(nil), cfg.MFAACR...), mfaAMR: append([]string(nil), cfg.MFAAMR...),
		},
		ttl:    cfg.CacheTTL,
		clock:  cfg.Clock,
		logger: cfg.Logger,
	}
	m.stepUp = realm + `, error="insufficient_user_authentication", error_description="multi-factor authentication required"`
	if len(cfg.MFAACR) > 0 {
		m.stepUp += `, acr_values="` + strings.Join(cfg.MFAACR, " ") + `"`
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
	return m, nil
}

// isClaimToken accepts values that are safe inside a quoted
// WWW-Authenticate parameter.
func isClaimToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c <= ' ' || c >= 0x7f || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

// Wrap returns next behind staff authentication: every request needs a
// valid staff token with MFA. There are no anonymous staff routes.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearer.Token(r, maxTokenLen)
		if !ok {
			unauthorized(w)
			return
		}
		p, err := m.authenticate(r.Context(), token)
		var rej rejection
		switch {
		case errors.As(err, &rej):
			if rej != rejectInactive {
				// A mismatch is a misconfiguration signal (wrong issuer,
				// audience or claim contract); the reason names no person.
				m.logger.Info("staff token rejected", "reason", string(rej))
			}
			unauthorized(w)
			return
		case err != nil:
			m.logger.Warn("staff token introspection unavailable", "error", err)
			httpapi.WriteProblem(w, http.StatusServiceUnavailable, "auth_unavailable", "Sign-in is temporarily unavailable.")
			return
		}
		if !p.MFA {
			w.Header().Set("WWW-Authenticate", m.stepUp)
			httpapi.WriteProblem(w, http.StatusForbidden, "mfa_required", "Sign in with a second factor.")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

// Unconfigured answers every request with 503 "staff_auth_not_configured".
// Wiring mounts it on staff routes when no provider is configured, so a
// member session can never stand in for staff authentication.
func Unconfigured() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "staff_auth_not_configured", "Staff sign-in is not configured.")
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", realm)
	httpapi.WriteProblem(w, http.StatusUnauthorized, "unauthenticated", "Sign in again.")
}

// rejection is why a token was refused (401). Its values go to the log.
type rejection string

func (r rejection) Error() string { return "staff token rejected: " + string(r) }

const (
	rejectInactive  rejection = "inactive"
	rejectKind      rejection = "token_kind"
	rejectIssuer    rejection = "issuer"
	rejectAudience  rejection = "audience"
	rejectExpiry    rejection = "expiry"
	rejectNoSubject rejection = "subject"
)

func (m *Middleware) authenticate(ctx context.Context, token string) (auth.Principal, error) {
	now := m.clock.Now()
	if p, ok := m.cache.Get(token, now); ok {
		return p, nil
	}
	c, err := m.introspect(ctx, token)
	if err != nil {
		return auth.Principal{}, err
	}
	if active, _ := c["active"].(bool); !active {
		return auth.Principal{}, rejectInactive
	}
	if !tokenKindOK(c) {
		return auth.Principal{}, rejectKind
	}
	if c.str("iss") != m.policy.issuer {
		return auth.Principal{}, rejectIssuer
	}
	if !m.policy.audienceOK(c) {
		return auth.Principal{}, rejectAudience
	}
	exp, ok := c.expiry()
	if !ok || !now.Before(exp) {
		return auth.Principal{}, rejectExpiry
	}
	sub := c.str("sub")
	if sub == "" {
		return auth.Principal{}, rejectNoSubject
	}
	p := auth.Principal{
		IdentityID: StaffID(m.policy.issuer, sub),
		Staff:      true,
		Roles:      m.policy.roles(c),
		MFA:        m.policy.mfa(c),
	}
	expires := now.Add(m.ttl)
	if exp.Before(expires) {
		expires = exp
	}
	// The principal is cached with its MFA flag; Wrap checks MFA on every
	// request, so a cached token without MFA keeps getting 403.
	m.cache.Put(token, p, expires)
	return p, nil
}

// introspect posts the token to the introspection endpoint (RFC 7662 § 2.1).
// The token travels only in the form body, never in the URL, so transport
// errors (which quote the URL) cannot leak it.
func (m *Middleware) introspect(ctx context.Context, token string) (claims, error) {
	form := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("introspection: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// RFC 6749 § 2.3.1: the client ID and secret are form-encoded before
	// Basic authentication.
	req.SetBasicAuth(url.QueryEscape(m.clientID), url.QueryEscape(m.clientSecret))
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("introspection: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := io.LimitReader(resp.Body, maxIntrospectionBody)
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, body)
		// 401/403 here means the provider refused our client credentials,
		// not the caller's token: an operator problem, hence 503.
		return nil, fmt.Errorf("introspection: status %d", resp.StatusCode)
	}
	dec := json.NewDecoder(body)
	dec.UseNumber()
	var c claims
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("introspection: decode: %w", err)
	}
	if c == nil {
		return nil, errors.New("introspection: empty response")
	}
	return c, nil
}
