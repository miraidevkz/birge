// Package ratelimit is an in-memory token-bucket limiter keyed by a string
// (for example the client IP) and an HTTP middleware that answers 429 with a
// problem body when a key runs out of tokens.
//
// Each key has a bucket of Rate.Burst tokens, refilled at one token per
// Rate.Every. The table of buckets is bounded by MaxKeys: buckets that have
// refilled completely carry no state and are dropped first; when every bucket
// is still active, an arbitrary one is dropped to make room. The limiter
// therefore fails open for a new key under a key-flooding attack instead of
// refusing everyone: on the API this limit protects capacity, and refusing all
// clients would also cut mothers off from the crisis endpoints (AGENTS.md,
// non-negotiable 1). Endpoints that send SMS have their own fail-closed limits
// (membership/authgw, notify/sms/guard).
//
// State lives in process memory, so limits hold per replica: with N replicas
// a client gets up to N times the rate.
//
// Keys are kept only in memory and never logged.
package ratelimit

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"birge/internal/platform/clock"
	"birge/internal/platform/httpapi"
)

// DefaultMaxKeys bounds the bucket table when Options.MaxKeys is zero.
const DefaultMaxKeys = 100_000

// Rate is a token bucket: Burst tokens, refilled at one token per Every.
type Rate struct {
	Burst int
	Every time.Duration
}

// Valid reports whether both fields are positive.
func (r Rate) Valid() bool { return r.Burst > 0 && r.Every > 0 }

// Options tune a Limiter. Zero values take the defaults.
type Options struct {
	// MaxKeys bounds the number of buckets kept. Default DefaultMaxKeys.
	MaxKeys int
	Clock   clock.Clock
}

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter keeps one token bucket per key. It is safe for concurrent use.
type Limiter struct {
	rate    Rate
	maxKeys int
	clock   clock.Clock

	mu      sync.Mutex
	buckets map[string]*bucket
}

// New returns a limiter for r.
func New(r Rate, opts Options) (*Limiter, error) {
	if !r.Valid() {
		return nil, errors.New("ratelimit: burst and interval must be positive")
	}
	if opts.MaxKeys <= 0 {
		opts.MaxKeys = DefaultMaxKeys
	}
	if opts.Clock == nil {
		opts.Clock = clock.System{}
	}
	return &Limiter{rate: r, maxKeys: opts.MaxKeys, clock: opts.Clock, buckets: make(map[string]*bucket)}, nil
}

// Allow takes one token for key and reports whether one was available.
func (l *Limiter) Allow(key string) bool {
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.makeRoom(now)
		}
		b = &bucket{tokens: float64(l.rate.Burst), last: now}
		l.buckets[key] = b
	}
	l.refill(b, now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RetryAfter is the whole number of seconds after which a refused key has a
// token again in the worst case (one refill interval), at least 1.
func (l *Limiter) RetryAfter() int {
	s := int((l.rate.Every + time.Second - 1) / time.Second)
	return max(s, 1)
}

func (l *Limiter) refill(b *bucket, now time.Time) {
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(float64(l.rate.Burst), b.tokens+float64(elapsed)/float64(l.rate.Every))
		b.last = now
	}
}

// makeRoom must be called with mu held. It drops full buckets; if none is
// full, it drops one arbitrary bucket (map order), which resets that key.
func (l *Limiter) makeRoom(now time.Time) {
	for k, b := range l.buckets {
		l.refill(b, now)
		if b.tokens >= float64(l.rate.Burst) {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) < l.maxKeys {
		return
	}
	for k := range l.buckets {
		delete(l.buckets, k)
		return
	}
}

// Middleware returns next behind the limiter. key derives the bucket key from
// the request (for example the client IP); a refused request gets 429 with
// Retry-After and the problem code "rate_limited".
func Middleware(l *Limiter, key func(*http.Request) string) func(http.Handler) http.Handler {
	retry := strconv.Itoa(l.RetryAfter())
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow(key(r)) {
				w.Header().Set("Retry-After", retry)
				httpapi.WriteProblem(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
