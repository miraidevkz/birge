package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrStoreFull reports that a MemoryStore holds MaxKeys live entries and has
// no room for a new key. Callers treat it like any store error (fail open
// or closed, see the package doc).
var ErrStoreFull = errors.New("ratelimit: memory store is full")

// Store keeps fixed-window counters and one-time claims. Implementations
// are safe for concurrent use; each call is atomic on its own.
type Store interface {
	// Hit counts one hit for key in the window of length window that
	// contains now (WindowStart), unless that window already holds limit
	// hits; limit <= 0 means no limit. It reports whether the hit was
	// counted. A refused hit is not counted.
	Hit(ctx context.Context, key string, window time.Duration, limit int, now time.Time) (bool, error)
	// Claim records key until expires and reports whether it was free: never
	// claimed, or its previous claim had expired at now.
	Claim(ctx context.Context, key string, expires, now time.Time) (bool, error)
}

// MemoryStore is a Store in process memory, for tests and a single replica:
// with several replicas each one counts on its own, so limits multiply by
// the replica count and a claim holds per replica only.
//
// It holds at most maxKeys counters and maxKeys claims. When a table is full
// it drops expired entries; if none expired it refuses the new key with
// ErrStoreFull instead of evicting a live one, so a key flood can never
// reset the counter of a key under attack.
type MemoryStore struct {
	maxKeys int

	mu       sync.Mutex
	counters map[counterKey]*counter
	claims   map[string]time.Time
}

type counterKey struct {
	key   string
	start int64 // window start, Unix nanoseconds
}

type counter struct {
	hits    int
	expires time.Time
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore returns an empty store of at most maxKeys counters and
// maxKeys claims (DefaultMaxKeys when maxKeys <= 0).
func NewMemoryStore(maxKeys int) *MemoryStore {
	if maxKeys <= 0 {
		maxKeys = DefaultMaxKeys
	}
	return &MemoryStore{maxKeys: maxKeys, counters: make(map[counterKey]*counter), claims: make(map[string]time.Time)}
}

// Hit implements Store.
func (m *MemoryStore) Hit(_ context.Context, key string, window time.Duration, limit int, now time.Time) (bool, error) {
	if window <= 0 {
		return false, errors.New("ratelimit: window must be positive")
	}
	start := WindowStart(now, window)
	k := counterKey{key: key, start: start.UnixNano()}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.counters[k]; ok {
		if limit > 0 && c.hits >= limit {
			return false, nil
		}
		c.hits++
		return true, nil
	}
	if len(m.counters) >= m.maxKeys {
		for ck, c := range m.counters {
			if !now.Before(c.expires) {
				delete(m.counters, ck)
			}
		}
		if len(m.counters) >= m.maxKeys {
			return false, ErrStoreFull
		}
	}
	m.counters[k] = &counter{hits: 1, expires: start.Add(window)}
	return true, nil
}

// Claim implements Store.
func (m *MemoryStore) Claim(_ context.Context, key string, expires, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if exp, ok := m.claims[key]; ok && now.Before(exp) {
		return false, nil
	}
	if len(m.claims) >= m.maxKeys {
		for k, exp := range m.claims {
			if !now.Before(exp) {
				delete(m.claims, k)
			}
		}
		if len(m.claims) >= m.maxKeys {
			return false, ErrStoreFull
		}
	}
	m.claims[key] = expires
	return true, nil
}
