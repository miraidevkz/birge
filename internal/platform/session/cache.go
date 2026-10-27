package session

import (
	"container/list"
	"sync"
	"time"

	"birge/internal/platform/auth"
)

// tokenKey is the SHA-256 of a session token; the raw token is never stored.
type tokenKey [32]byte

type cacheEntry struct {
	key       tokenKey
	principal auth.Principal
	expires   time.Time
}

// lru is a bounded least-recently-used cache of validated sessions.
type lru struct {
	mu    sync.Mutex
	size  int
	order *list.List // front = most recently used; values are *cacheEntry
	items map[tokenKey]*list.Element
}

func newLRU(size int) *lru {
	return &lru{size: size, order: list.New(), items: make(map[tokenKey]*list.Element, size)}
}

// get returns the principal for k if it is cached and not expired at now.
func (c *lru) get(k tokenKey, now time.Time) (auth.Principal, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return auth.Principal{}, false
	}
	e := el.Value.(*cacheEntry)
	if !now.Before(e.expires) {
		c.order.Remove(el)
		delete(c.items, k)
		return auth.Principal{}, false
	}
	c.order.MoveToFront(el)
	return e.principal, true
}

// put stores p under k until expires, evicting the least recently used entry when full.
func (c *lru) put(k tokenKey, p auth.Principal, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		e := el.Value.(*cacheEntry)
		e.principal, e.expires = p, expires
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&cacheEntry{key: k, principal: p, expires: expires})
	for c.order.Len() > c.size {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*cacheEntry).key)
	}
}

func (c *lru) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
