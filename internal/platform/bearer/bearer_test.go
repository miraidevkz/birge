package bearer

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestToken(t *testing.T) {
	tests := []struct {
		name, header string
		want         string
		ok           bool
	}{
		{"valid", "Bearer tok-1", "tok-1", true},
		{"scheme case-insensitive", "bearer tok-1", "tok-1", true},
		{"missing", "", "", false},
		{"basic", "Basic dXNlcjpwYXNz", "", false},
		{"empty", "Bearer ", "", false},
		{"control char", "Bearer a\tb", "", false},
		{"non-ascii", "Bearer aé", "", false},
		{"too long", "Bearer " + strings.Repeat("a", 17), "", false},
		{"at limit", "Bearer " + strings.Repeat("a", 16), strings.Repeat("a", 16), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			got, ok := Token(r, 16)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("Token = %q, %t; want %q, %t", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCacheBounded(t *testing.T) {
	c := NewCache[int](2)
	now := time.Now()
	c.Put("a", 1, now.Add(time.Minute))
	c.Put("b", 2, now.Add(time.Minute))
	c.Get("a", now) // a is now most recent
	c.Put("c", 3, now.Add(time.Minute))
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
	if _, ok := c.Get("b", now); ok {
		t.Error("least recently used entry not evicted")
	}
	if v, ok := c.Get("a", now); !ok || v != 1 {
		t.Errorf("recent entry = %d, %t", v, ok)
	}
	if _, ok := c.Get("c", now.Add(2*time.Minute)); ok {
		t.Error("expired entry returned")
	}
	c.Put("a", 10, now.Add(time.Minute))
	if v, _ := c.Get("a", now); v != 10 || c.Len() != 1 {
		t.Errorf("update: value %d, len %d", v, c.Len())
	}
}

func TestCacheKeyedByHash(t *testing.T) {
	const token = "tok-secret-0000000000"
	c := NewCache[int](4)
	c.Put(token, 1, time.Now().Add(time.Minute))
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.items {
		if bytes.Contains(k[:], []byte(token[:8])) {
			t.Fatal("cache key contains the raw token")
		}
	}
}
