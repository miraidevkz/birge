package authgw

import (
	"sync"
	"time"
)

// Rate is a token bucket: Burst tokens, refilled at one token per Every.
type Rate struct {
	Burst int
	Every time.Duration
}

func (r Rate) valid() bool { return r.Burst > 0 && r.Every > 0 }

type bucket struct {
	tokens float64
	last   time.Time
}

// limiter keeps one token bucket per key in memory, bounded to maxKeys.
type limiter struct {
	mu      sync.Mutex
	rate    Rate
	maxKeys int
	buckets map[string]*bucket
}

func newLimiter(r Rate, maxKeys int) *limiter {
	return &limiter{rate: r, maxKeys: maxKeys, buckets: make(map[string]*bucket)}
}

// allow takes one token for key at now and reports whether one was available.
// When the table is full of active buckets it refuses (fails closed).
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.sweep(now)
			if len(l.buckets) >= l.maxKeys {
				return false
			}
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

func (l *limiter) refill(b *bucket, now time.Time) {
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += float64(elapsed) / float64(l.rate.Every)
		if burst := float64(l.rate.Burst); b.tokens > burst {
			b.tokens = burst
		}
		b.last = now
	}
}

// sweep drops buckets that have refilled completely; they carry no state.
func (l *limiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		l.refill(b, now)
		if b.tokens >= float64(l.rate.Burst) {
			delete(l.buckets, k)
		}
	}
}
