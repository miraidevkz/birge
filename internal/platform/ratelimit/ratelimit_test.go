package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestLimiter(t *testing.T, r Rate, maxKeys int) (*Limiter, *fakeClock) {
	t.Helper()
	clk := &fakeClock{now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	l, err := New(r, Options{MaxKeys: maxKeys, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	return l, clk
}

func TestBurstThenRefill(t *testing.T) {
	l, clk := newTestLimiter(t, Rate{Burst: 3, Every: time.Second}, 10)
	for i := range 3 {
		if !l.Allow("a") {
			t.Fatalf("request %d refused within burst", i)
		}
	}
	if l.Allow("a") {
		t.Fatal("request beyond burst allowed")
	}
	if !l.Allow("b") {
		t.Fatal("other key limited by a's bucket")
	}
	clk.advance(999 * time.Millisecond)
	if l.Allow("a") {
		t.Fatal("token refilled too early")
	}
	clk.advance(time.Millisecond)
	if !l.Allow("a") {
		t.Fatal("token not refilled after one interval")
	}
	// A long pause refills to the burst, not beyond.
	clk.advance(time.Hour)
	for range 3 {
		if !l.Allow("a") {
			t.Fatal("burst not restored")
		}
	}
	if l.Allow("a") {
		t.Fatal("bucket grew beyond burst")
	}
}

func TestTableBounded(t *testing.T) {
	l, clk := newTestLimiter(t, Rate{Burst: 1, Every: time.Minute}, 2)
	l.Allow("a")
	l.Allow("b")
	l.Allow("c") // table full of active buckets: one is dropped, c is admitted
	if n := len(l.buckets); n != 2 {
		t.Fatalf("buckets = %d, want 2", n)
	}
	// Full buckets are dropped before active ones.
	clk.advance(time.Hour)
	l.Allow("d")
	if n := len(l.buckets); n != 1 {
		t.Fatalf("buckets after sweep = %d, want 1", n)
	}
}

func TestNewRejectsInvalidRate(t *testing.T) {
	for _, r := range []Rate{{}, {Burst: 1}, {Every: time.Second}, {Burst: -1, Every: time.Second}} {
		if _, err := New(r, Options{}); err == nil {
			t.Errorf("New(%+v) accepted", r)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	for _, tt := range []struct {
		every time.Duration
		want  int
	}{{100 * time.Millisecond, 1}, {time.Second, 1}, {1500 * time.Millisecond, 2}, {time.Minute, 60}} {
		l, _ := newTestLimiter(t, Rate{Burst: 1, Every: tt.every}, 1)
		if got := l.RetryAfter(); got != tt.want {
			t.Errorf("RetryAfter(%s) = %d, want %d", tt.every, got, tt.want)
		}
	}
}

func TestMiddleware(t *testing.T) {
	l, _ := newTestLimiter(t, Rate{Burst: 1, Every: time.Minute}, 10)
	h := Middleware(l, func(r *http.Request) string { return r.Header.Get("X-Key") })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	do := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/x", nil)
		req.Header.Set("X-Key", key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("a"); rec.Code != http.StatusNoContent {
		t.Fatalf("first request: %d", rec.Code)
	}
	rec := do("a")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "60" || !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Errorf("429 response: Retry-After %q, body %s", rec.Header().Get("Retry-After"), rec.Body)
	}
	if rec := do("b"); rec.Code != http.StatusNoContent {
		t.Fatalf("other key: %d", rec.Code)
	}
}
