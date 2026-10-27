// Package bearer holds what the member session middleware (package session)
// and the staff token middleware (package staffauth) share: parsing
// "Authorization: Bearer <token>" and a bounded cache of validated tokens
// (ADR-0008).
//
// Invariant: the cache never stores a raw token. Entries are keyed by the
// SHA-256 of the token, so a heap dump or a debugging print of the cache
// cannot be replayed as a credential.
package bearer

import (
	"container/list"
	"crypto/sha256"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Token extracts a syntactically plausible bearer token from the
// Authorization header: scheme "Bearer" (any case), non-empty, at most maxLen
// bytes, printable ASCII without spaces. Anything else is reported as
// missing, so a malformed header never reaches the identity provider.
func Token(r *http.Request, maxLen int) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxLen {
		return "", false
	}
	for i := 0; i < len(token); i++ {
		if c := token[i]; c <= ' ' || c >= 0x7f {
			return "", false
		}
	}
	return token, true
}

// key is the SHA-256 of a token.
type key [32]byte

type entry[V any] struct {
	key     key
	value   V
	expires time.Time
}

// Cache is a bounded least-recently-used cache of values derived from
// validated tokens (a principal). It is safe for concurrent use.
type Cache[V any] struct {
	mu    sync.Mutex
	size  int
	order *list.List // front = most recently used; values are *entry[V]
	items map[key]*list.Element
}

// NewCache returns a cache holding at most size entries (at least one).
func NewCache[V any](size int) *Cache[V] {
	if size < 1 {
		size = 1
	}
	return &Cache[V]{size: size, order: list.New(), items: make(map[key]*list.Element, size)}
}

// Get returns the value cached for token if it has not expired at now.
// An expired entry is removed.
func (c *Cache[V]) Get(token string, now time.Time) (V, bool) {
	k := key(sha256.Sum256([]byte(token)))
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.items[k]
	if !ok {
		return zero, false
	}
	e := el.Value.(*entry[V])
	if !now.Before(e.expires) {
		c.order.Remove(el)
		delete(c.items, k)
		return zero, false
	}
	c.order.MoveToFront(el)
	return e.value, true
}

// Put stores v for token until expires, evicting the least recently used
// entry when the cache is full.
func (c *Cache[V]) Put(token string, v V, expires time.Time) {
	k := key(sha256.Sum256([]byte(token)))
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		e := el.Value.(*entry[V])
		e.value, e.expires = v, expires
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&entry[V]{key: k, value: v, expires: expires})
	for c.order.Len() > c.size {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*entry[V]).key)
	}
}

// Len returns the number of cached entries, expired ones included until
// they are looked up or evicted.
func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
