package ratelimit

import (
	"context"
	"errors"
	"log/slog"
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

// testStart is the start of a window for every test window length used here.
var testStart = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func newTestLimiter(t *testing.T, r Rate, s Store) (*Limiter, *fakeClock) {
	t.Helper()
	clk := &fakeClock{now: testStart}
	l, err := New("test", r, Options{Store: s, Clock: clk, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	return l, clk
}

func allow(t *testing.T, l *Limiter, key string) bool {
	t.Helper()
	ok, err := l.Allow(context.Background(), key)
	if err != nil {
		t.Fatalf("Allow(%q): %v", key, err)
	}
	return ok
}

func TestFixedWindow(t *testing.T) {
	// 3 per window of 3s.
	l, clk := newTestLimiter(t, Rate{Burst: 3, Every: time.Second}, nil)
	for i := range 3 {
		if !allow(t, l, "a") {
			t.Fatalf("request %d refused within burst", i)
		}
	}
	if allow(t, l, "a") {
		t.Fatal("request beyond burst allowed")
	}
	if !allow(t, l, "b") {
		t.Fatal("other key limited by a's counter")
	}
	clk.advance(2999 * time.Millisecond)
	if allow(t, l, "a") {
		t.Fatal("allowed before the window ended")
	}
	clk.advance(time.Millisecond)
	for range 3 {
		if !allow(t, l, "a") {
			t.Fatal("burst not restored in the next window")
		}
	}
	if allow(t, l, "a") {
		t.Fatal("new window grew beyond burst")
	}
}

func TestRefusedHitsAreNotCounted(t *testing.T) {
	s := NewMemoryStore(10)
	ctx := context.Background()
	for range 5 {
		_, _ = s.Hit(ctx, "k", time.Minute, 1, testStart)
	}
	if c := s.counters[counterKey{key: "k", start: testStart.UnixNano()}]; c.hits != 1 {
		t.Fatalf("hits = %d, want 1 (refusals must not count)", c.hits)
	}
	// No limit counts every hit.
	for range 3 {
		if ok, _ := s.Hit(ctx, "n", time.Minute, 0, testStart); !ok {
			t.Fatal("unlimited hit refused")
		}
	}
}

func TestMemoryStoreBounded(t *testing.T) {
	s := NewMemoryStore(2)
	ctx := context.Background()
	for _, k := range []string{"a", "b"} {
		if ok, err := s.Hit(ctx, k, time.Minute, 1, testStart); !ok || err != nil {
			t.Fatalf("Hit(%s) = %v, %v", k, ok, err)
		}
	}
	// Full of live counters: a new key is refused, never by evicting a live one.
	if _, err := s.Hit(ctx, "c", time.Minute, 1, testStart); !errors.Is(err, ErrStoreFull) {
		t.Fatalf("Hit(c) err = %v, want ErrStoreFull", err)
	}
	if ok, _ := s.Hit(ctx, "a", time.Minute, 1, testStart); ok {
		t.Fatal("a's counter was reset")
	}
	// Expired counters make room.
	later := testStart.Add(time.Minute)
	if ok, err := s.Hit(ctx, "c", time.Minute, 1, later); !ok || err != nil {
		t.Fatalf("Hit(c) after expiry = %v, %v", ok, err)
	}
	if n := len(s.counters); n != 1 {
		t.Fatalf("counters after sweep = %d, want 1", n)
	}
}

func TestMemoryClaim(t *testing.T) {
	s := NewMemoryStore(2)
	ctx := context.Background()
	exp := testStart.Add(5 * time.Minute)
	if ok, err := s.Claim(ctx, "x", exp, testStart); !ok || err != nil {
		t.Fatalf("first claim = %v, %v", ok, err)
	}
	if ok, _ := s.Claim(ctx, "x", exp, testStart.Add(time.Minute)); ok {
		t.Fatal("live claim taken twice")
	}
	if ok, _ := s.Claim(ctx, "y", exp, testStart); !ok {
		t.Fatal("second key refused")
	}
	if _, err := s.Claim(ctx, "z", exp, testStart); !errors.Is(err, ErrStoreFull) {
		t.Fatalf("claim in a full store: %v, want ErrStoreFull", err)
	}
	// After expiry the key is free again and the table has room.
	if ok, err := s.Claim(ctx, "z", exp.Add(5*time.Minute), exp); !ok || err != nil {
		t.Fatalf("claim after expiry = %v, %v", ok, err)
	}
}

func TestNewRejectsInvalid(t *testing.T) {
	for _, r := range []Rate{{}, {Burst: 1}, {Every: time.Second}, {Burst: -1, Every: time.Second}} {
		if _, err := New("x", r, Options{}); err == nil {
			t.Errorf("New(%+v) accepted", r)
		}
	}
	for _, name := range []string{"", "a|b"} {
		if _, err := New(name, Rate{Burst: 1, Every: time.Second}, Options{}); err == nil {
			t.Errorf("New(name %q) accepted", name)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	for _, tt := range []struct {
		r       Rate
		elapsed time.Duration
		want    int
	}{
		{Rate{Burst: 1, Every: 100 * time.Millisecond}, 0, 1},
		{Rate{Burst: 1, Every: time.Minute}, 0, 60},
		{Rate{Burst: 2, Every: time.Minute}, 90 * time.Second, 30},
	} {
		l, clk := newTestLimiter(t, tt.r, nil)
		clk.advance(tt.elapsed)
		if got := l.RetryAfter(); got != tt.want {
			t.Errorf("RetryAfter(%+v after %s) = %d, want %d", tt.r, tt.elapsed, got, tt.want)
		}
	}
}

func TestWindowStartIsUTCDay(t *testing.T) {
	now := time.Date(2026, 10, 27, 23, 59, 0, 0, time.FixedZone("Almaty", 5*3600))
	got := WindowStart(now, 24*time.Hour)
	if want := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("WindowStart = %s, want %s", got, want)
	}
}

// failingStore fails every call.
type failingStore struct{}

func (failingStore) Hit(context.Context, string, time.Duration, int, time.Time) (bool, error) {
	return false, errors.New("store down")
}

func (failingStore) Claim(context.Context, string, time.Time, time.Time) (bool, error) {
	return false, errors.New("store down")
}

func middlewareDo(h http.Handler, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/x", nil)
	req.Header.Set("X-Key", key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMiddleware(t *testing.T) {
	l, _ := newTestLimiter(t, Rate{Burst: 1, Every: time.Minute}, nil)
	h := Middleware(l, func(r *http.Request) string { return r.Header.Get("X-Key") })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	if rec := middlewareDo(h, "a"); rec.Code != http.StatusNoContent {
		t.Fatalf("first request: %d", rec.Code)
	}
	rec := middlewareDo(h, "a")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "60" || !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Errorf("429 response: Retry-After %q, body %s", rec.Header().Get("Retry-After"), rec.Body)
	}
	if rec := middlewareDo(h, "b"); rec.Code != http.StatusNoContent {
		t.Fatalf("other key: %d", rec.Code)
	}
}

func TestMiddlewareFailsOpen(t *testing.T) {
	var logs strings.Builder
	l, err := New("api.ip", Rate{Burst: 1, Every: time.Minute}, Options{
		Store: failingStore{}, Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := Middleware(l, func(r *http.Request) string { return r.Header.Get("X-Key") })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	if rec := middlewareDo(h, "203.0.113.7"); rec.Code != http.StatusNoContent {
		t.Fatalf("store down: %d, want the request through", rec.Code)
	}
	if !strings.Contains(logs.String(), "RateLimitStoreError") || strings.Contains(logs.String(), "203.0.113.7") {
		t.Errorf("log: want the event without the key: %s", logs.String())
	}
}
