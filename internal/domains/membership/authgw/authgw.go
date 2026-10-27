// Package authgw is the member sign-in gateway in front of the Kratos public
// API (ADR-0008, addendum 2026-10-27; threat model § 1: SMS pumping, account
// takeover). Kratos is never exposed directly: the gateway is mounted at
// /auth/ and forwards only an allowlist of native (API) flow endpoints.
//
// Every submission to the login and registration flows (POST, the requests
// that make Kratos send an SMS code or check one) must pass, in order:
//
//  1. the per-IP rate limit (applied to every gateway request);
//  2. a proof-of-work solution in the X-Pow-Solution header: an ALTCHA
//     payload for a challenge from GET /auth/pow (SHA-256 over salt+number,
//     HMAC-SHA-256 signature, valid 5 minutes, accepted once);
//  3. a JSON body for the code method with a phone number in E.164 form
//     whose prefix is in Config.AllowedPrefixes (login: "identifier" and
//     optional "address"; registration: traits.<Config.PhoneTrait>);
//  4. the per-number and the global rate limits.
//
// A request that fails a check is answered here and never reaches Kratos.
// The checked body is re-encoded before forwarding so Kratos sees exactly the
// values that were checked.
//
// Limits of this implementation: the PoW replay store and the rate limiters
// live in memory, so they hold for one replica only; with more replicas a
// solution can be replayed once per replica and limits multiply by the
// replica count. Move them to a shared store before scaling out.
//
// Logs carry reasons and a keyed hash of the number (number_hash), never the
// number, the code, the body or the session token.
package authgw

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"birge/internal/platform/clock"
	"birge/internal/platform/httpapi"
)

// PoWHeader carries the base64 ALTCHA solution on flow submissions.
const PoWHeader = "X-Pow-Solution"

const (
	defaultMaxBody         = 16 << 10
	defaultUpstreamTimeout = 10 * time.Second
	defaultMaxKeys         = 100_000
	defaultPhoneTrait      = "phone"
	minKeyLen              = 32
)

// Config configures the gateway. Keys come from the secret store.
type Config struct {
	// KratosPublicURL is the cluster-internal base URL of the Kratos public API.
	KratosPublicURL string
	// PoWKey signs challenges (HMAC-SHA-256), at least 32 bytes.
	PoWKey []byte
	// PoWMaxNumber is the largest secret number in a challenge; it sets the work.
	PoWMaxNumber int64
	// HashKey keys the hash of phone numbers and IPs used in logs and limiter
	// tables, at least 32 bytes. Must differ from PoWKey.
	HashKey []byte
	// AllowedPrefixes are E.164 prefixes of allowed mobile ranges, e.g. "+7700".
	AllowedPrefixes []string
	// PhoneTrait is the identity trait holding the phone in registration. Default "phone".
	PhoneTrait string
	// PerIP, PerNumber and Global are the token buckets; all are required.
	PerIP, PerNumber, Global Rate
	// MaxKeys bounds each limiter table and the replay store. Default 100000.
	MaxKeys int
	// MaxBodyBytes limits request bodies. Default 16 KiB.
	MaxBodyBytes int64
	// UpstreamTimeout bounds one proxied request. Default 10s.
	UpstreamTimeout time.Duration
	// ClientIP returns the caller's address. Default: the host of RemoteAddr.
	// Behind an ingress, pass a function that reads the trusted proxy header.
	ClientIP func(*http.Request) string
	// Transport reaches Kratos. Optional.
	Transport http.RoundTripper
	Clock     clock.Clock
	Logger    *slog.Logger
}

// Gateway is the http.Handler mounted at /auth/.
type Gateway struct {
	cfg       Config
	pow       *pow
	perIP     *limiter
	perNumber *limiter
	global    *limiter
	proxy     *httputil.ReverseProxy
	mux       *http.ServeMux
}

// New validates cfg and builds the gateway.
func New(cfg Config) (*Gateway, error) {
	target, err := url.Parse(cfg.KratosPublicURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, errors.New("authgw: KratosPublicURL must be an absolute http(s) URL")
	}
	switch {
	case len(cfg.PoWKey) < minKeyLen || len(cfg.HashKey) < minKeyLen:
		return nil, errors.New("authgw: PoWKey and HashKey must be at least 32 bytes")
	case hmac.Equal(cfg.PoWKey, cfg.HashKey):
		return nil, errors.New("authgw: PoWKey and HashKey must differ")
	case cfg.PoWMaxNumber <= 0:
		return nil, errors.New("authgw: PoWMaxNumber must be positive")
	case len(cfg.AllowedPrefixes) == 0:
		return nil, errors.New("authgw: AllowedPrefixes is empty")
	case !cfg.PerIP.valid() || !cfg.PerNumber.valid() || !cfg.Global.valid():
		return nil, errors.New("authgw: PerIP, PerNumber and Global rates are required")
	}
	for _, p := range cfg.AllowedPrefixes {
		if len(p) < 2 || p[0] != '+' || strings.Trim(p[1:], "0123456789") != "" {
			return nil, errors.New("authgw: AllowedPrefixes must be '+' followed by digits")
		}
	}
	if cfg.PhoneTrait == "" {
		cfg.PhoneTrait = defaultPhoneTrait
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = defaultMaxKeys
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaultMaxBody
	}
	if cfg.UpstreamTimeout <= 0 {
		cfg.UpstreamTimeout = defaultUpstreamTimeout
	}
	if cfg.ClientIP == nil {
		cfg.ClientIP = remoteHost
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.System{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Transport == nil {
		cfg.Transport = defaultTransport()
	}

	g := &Gateway{
		cfg:       cfg,
		pow:       &pow{key: cfg.PoWKey, maxNumber: cfg.PoWMaxNumber, replay: newReplayStore(cfg.MaxKeys)},
		perIP:     newLimiter(cfg.PerIP, cfg.MaxKeys),
		perNumber: newLimiter(cfg.PerNumber, cfg.MaxKeys),
		global:    newLimiter(cfg.Global, 1),
	}
	g.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/auth")
			pr.Out.URL.RawPath = ""
			pr.SetURL(target)
			pr.Out.Header.Del(PoWHeader)
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set("X-Forwarded-For", cfg.ClientIP(pr.In))
		},
		Transport:    cfg.Transport,
		ErrorHandler: g.upstreamError,
	}
	g.mux = g.routes()
	return g, nil
}

func defaultTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   32,
	}
}

// routes is the allowlist of Kratos endpoints reachable through the gateway:
// native login and registration flows, logout and the caller's own sessions.
// Everything else (settings, recovery, verification, admin) is 404.
func (g *Gateway) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/pow", g.issueChallenge)
	for _, p := range []string{
		"GET /auth/self-service/login/api",
		"GET /auth/self-service/login/flows",
		"GET /auth/self-service/registration/api",
		"GET /auth/self-service/registration/flows",
		"DELETE /auth/self-service/logout/api",
		"GET /auth/sessions/whoami",
		"GET /auth/sessions",
		"DELETE /auth/sessions",
		"DELETE /auth/sessions/{id}",
	} {
		mux.Handle(p, g.proxy)
	}
	mux.HandleFunc("POST /auth/self-service/login", g.submitFlow(false))
	mux.HandleFunc("POST /auth/self-service/registration", g.submitFlow(true))
	mux.HandleFunc("/auth/", func(w http.ResponseWriter, _ *http.Request) {
		httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "Not found.")
	})
	return mux
}

// ServeHTTP applies the per-IP limit, the body limit and the upstream timeout, then routes.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.perIP.allow(g.keyedHash("ip", g.cfg.ClientIP(r)), g.cfg.Clock.Now()) {
		g.reject(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", "ip_limit", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, g.cfg.MaxBodyBytes)
	ctx, cancel := context.WithTimeout(r.Context(), g.cfg.UpstreamTimeout)
	defer cancel()
	g.mux.ServeHTTP(w, r.WithContext(ctx))
}

func (g *Gateway) issueChallenge(w http.ResponseWriter, _ *http.Request) {
	c, err := g.pow.issue(g.cfg.Clock.Now())
	if err != nil {
		g.cfg.Logger.Error("auth gateway: issue pow challenge", "error", err)
		httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(c)
}

func (g *Gateway) submitFlow(registration bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := g.cfg.Clock.Now()
		if err := g.pow.verify(r.Header.Get(PoWHeader), now); err != nil {
			g.reject(w, http.StatusForbidden, "pow_required", "Solve the challenge and try again.", "pow: "+err.Error(), "")
			return
		}
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			g.reject(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Send JSON.", "content type", "")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				g.reject(w, http.StatusRequestEntityTooLarge, "body_too_large", "The request is too large.", "body size", "")
				return
			}
			g.reject(w, http.StatusBadRequest, "invalid_request", "The request is invalid.", "read body", "")
			return
		}
		numbers, canonical, err := flowNumbers(body, registration, g.cfg.PhoneTrait)
		if err != nil {
			g.reject(w, http.StatusBadRequest, "invalid_phone", "Enter a valid mobile number.", err.Error(), "")
			return
		}
		for _, n := range numbers {
			if err := checkNumber(n, g.cfg.AllowedPrefixes); err != nil {
				g.reject(w, http.StatusBadRequest, "invalid_phone", "Enter a valid mobile number.", err.Error(), g.keyedHash("phone", n))
				return
			}
		}
		for _, n := range numbers {
			if !g.perNumber.allow(g.keyedHash("phone", n), now) {
				g.reject(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", "number_limit", g.keyedHash("phone", n))
				return
			}
		}
		if !g.global.allow("global", now) {
			g.reject(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", "global_limit", "")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(canonical))
		r.ContentLength = int64(len(canonical))
		r.Header.Del("Content-Length")
		g.proxy.ServeHTTP(w, r)
	}
}

// reject answers with a problem and logs the reason; numberHash is optional.
func (g *Gateway) reject(w http.ResponseWriter, status int, code, title, reason, numberHash string) {
	attrs := []any{"status", status, "reason", reason}
	if numberHash != "" {
		attrs = append(attrs, "number_hash", numberHash)
	}
	g.cfg.Logger.Info("auth gateway rejected request", attrs...)
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	httpapi.WriteProblem(w, status, code, title)
}

func (g *Gateway) upstreamError(w http.ResponseWriter, _ *http.Request, err error) {
	g.cfg.Logger.Warn("auth gateway: kratos unreachable", "error", err)
	httpapi.WriteProblem(w, http.StatusBadGateway, "auth_unavailable", "Sign-in is temporarily unavailable.")
}

// keyedHash is a short HMAC-SHA-256 of value under HashKey, domain-separated
// by kind; it identifies a number or IP in logs and limiter tables without
// revealing it.
func (g *Gateway) keyedHash(kind, value string) string {
	m := hmac.New(sha256.New, g.cfg.HashKey)
	m.Write([]byte(kind))
	m.Write([]byte{0})
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
