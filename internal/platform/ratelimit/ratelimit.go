// Package ratelimit holds fixed-window rate limits and one-time claims, kept
// in a Store that is either process memory (MemoryStore: tests and a single
// replica) or PostgreSQL shared by every replica (PostgresStore). With the
// shared store, N API or worker replicas behave like one: a limit is counted
// once across all of them, and a claimed key (a proof-of-work solution) is
// accepted by one replica only. Wiring picks the store from
// BIRGE_RATE_LIMIT_STORE; see README.md for the design and its trade-offs.
//
// A Limiter allows Rate.Burst hits per key in each fixed window of
// Rate.Burst × Rate.Every (the same average rate as a token bucket of that
// burst and refill interval). Windows are aligned to absolute time, so every
// replica computes the same window. A refused hit is not counted. At a
// window boundary a client can get up to twice the burst in a short time;
// the limits here protect capacity and providers, and that is acceptable.
//
// Store errors (database down, memory table full) are returned to the
// caller, who decides: Middleware fails open, because it protects capacity
// and refusing every client would also cut mothers off from the crisis
// endpoints (AGENTS.md, non-negotiable 1); the auth gateway and the SMS guard
// fail closed, except that a critical SMS is never blocked by a store error.
//
// Keys may be IPs or phone numbers. MemoryStore keeps them in process memory
// only; PostgresStore stores only a keyed hash of them. Keys are never logged.
package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"birge/internal/platform/clock"
	"birge/internal/platform/httpapi"
)

// DefaultMaxKeys bounds the private MemoryStore of a limiter when
// Options.MaxKeys is zero.
const DefaultMaxKeys = 100_000

// Rate is Burst hits per window of Burst × Every: on average one hit per
// Every, with at most Burst at once. It is configured as "<burst>/<interval>".
type Rate struct {
	Burst int
	Every time.Duration
}

// Valid reports whether both fields are positive.
func (r Rate) Valid() bool { return r.Burst > 0 && r.Every > 0 }

// Window is the length of one fixed window, Burst × Every.
func (r Rate) Window() time.Duration { return time.Duration(r.Burst) * r.Every }

// Options tune a Limiter. Zero values take the defaults.
type Options struct {
	// Store keeps the counters. Nil gives the limiter a private MemoryStore
	// of MaxKeys keys (one replica only).
	Store Store
	// MaxKeys bounds the private MemoryStore. Default DefaultMaxKeys.
	MaxKeys int
	Clock   clock.Clock
	// Logger receives Middleware's store failures. Default slog.Default().
	Logger *slog.Logger
}

// Limiter counts hits per key in fixed windows. It is safe for concurrent use.
type Limiter struct {
	name   string
	rate   Rate
	store  Store
	clock  clock.Clock
	logger *slog.Logger
}

// New returns a limiter for r. name separates its keys from those of other
// limiters in a shared store; it must be unique per limit, e.g. "api.ip".
func New(name string, r Rate, opts Options) (*Limiter, error) {
	if !r.Valid() {
		return nil, errors.New("ratelimit: burst and interval must be positive")
	}
	if name == "" || strings.Contains(name, "|") {
		return nil, errors.New("ratelimit: name must be non-empty and must not contain '|'")
	}
	if opts.Store == nil {
		opts.Store = NewMemoryStore(opts.MaxKeys)
	}
	if opts.Clock == nil {
		opts.Clock = clock.System{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Limiter{name: name, rate: r, store: opts.Store, clock: opts.Clock, logger: opts.Logger}, nil
}

// Allow counts one hit for key and reports whether it is within the limit.
// On a store error it returns false with the error; the caller decides
// whether to fail open or closed.
func (l *Limiter) Allow(ctx context.Context, key string) (bool, error) {
	return l.store.Hit(ctx, l.name+"|"+key, l.rate.Window(), l.rate.Burst, l.clock.Now())
}

// RetryAfter is the whole number of seconds until the current window ends,
// at least 1: after it a refused key has its full burst again.
func (l *Limiter) RetryAfter() int {
	now := l.clock.Now()
	w := l.rate.Window()
	left := WindowStart(now, w).Add(w).Sub(now)
	return max(int((left+time.Second-1)/time.Second), 1)
}

// WindowStart is the start of the fixed window of length window that
// contains now. Windows are aligned to the Unix epoch, so every replica
// (and a day-long window in UTC) agrees on them.
func WindowStart(now time.Time, window time.Duration) time.Time {
	n := now.UnixNano()
	return time.Unix(0, n-n%int64(window)).UTC()
}

// Middleware returns next behind the limiter. key derives the limiter key
// from the request (for example the client IP); a refused request gets 429
// with Retry-After and the problem code "rate_limited". A store error lets
// the request through (fail open, see the package doc) and is logged with
// the limiter name only.
func Middleware(l *Limiter, key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, err := l.Allow(r.Context(), key(r))
			if err != nil {
				l.logger.Warn("rate limit store unavailable, request allowed",
					"event", "RateLimitStoreError", "limiter", l.name, "error", err)
				ok = true
			}
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(l.RetryAfter()))
				httpapi.WriteProblem(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
