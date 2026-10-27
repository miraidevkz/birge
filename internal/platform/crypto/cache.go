package crypto

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tink-crypto/tink-go/v2/tink"
)

// dekCache keeps unwrapped data keys for a short time, bounded in size. A
// shredded marker stops a load that raced with Shred from caching the old key.
type dekCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	entries map[uuid.UUID]cacheEntry
}

type cacheEntry struct {
	aead     tink.AEAD // nil for a shredded marker
	shredded bool
	expires  time.Time
}

type cacheState int

const (
	cacheMiss cacheState = iota
	cacheHit
	cacheShredded
)

func newDEKCache(ttl time.Duration, maxEntries int) *dekCache {
	return &dekCache{ttl: ttl, max: maxEntries, entries: make(map[uuid.UUID]cacheEntry)}
}

func (c *dekCache) get(id uuid.UUID, now time.Time) (tink.AEAD, cacheState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	switch {
	case !ok:
		return nil, cacheMiss
	case !now.Before(e.expires):
		delete(c.entries, id)
		return nil, cacheMiss
	case e.shredded:
		return nil, cacheShredded
	default:
		return e.aead, cacheHit
	}
}

// put caches a key unless the member is marked as shredded.
func (c *dekCache) put(id uuid.UUID, a tink.AEAD, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[id]; ok && e.shredded && now.Before(e.expires) {
		return
	}
	c.store(id, cacheEntry{aead: a, expires: now.Add(c.ttl)}, now)
}

// markShredded replaces any cached key of the member with a shredded marker.
func (c *dekCache) markShredded(id uuid.UUID, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store(id, cacheEntry{shredded: true, expires: now.Add(c.ttl)}, now)
}

func (c *dekCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// store must be called with mu held.
func (c *dekCache) store(id uuid.UUID, e cacheEntry, now time.Time) {
	if _, ok := c.entries[id]; !ok && len(c.entries) >= c.max {
		c.evict(now)
	}
	c.entries[id] = e
}

// evict drops expired entries, then live keys, then shredded markers, until
// there is room for one more entry. Map order makes the choice arbitrary.
func (c *dekCache) evict(now time.Time) {
	for id, e := range c.entries {
		if !now.Before(e.expires) {
			delete(c.entries, id)
		}
	}
	for _, markers := range []bool{false, true} {
		for id, e := range c.entries {
			if len(c.entries) < c.max {
				return
			}
			if e.shredded == markers {
				delete(c.entries, id)
			}
		}
	}
}
