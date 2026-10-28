package ratelimit_test

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"birge/internal/platform/ratelimit"
	"birge/internal/platform/testinfra"
)

var (
	hashKey = bytes.Repeat([]byte("k"), 32)
	start   = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
)

func TestPostgresStoreSharedAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	// Two stores on one database stand for two replicas.
	a, err := ratelimit.NewPostgresStore(pool, hashKey)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ratelimit.NewPostgresStore(pool, hashKey)
	if err != nil {
		t.Fatal(err)
	}

	// 20 concurrent hits split over both replicas, limit 7: exactly 7 pass.
	var passed atomic.Int64
	var wg sync.WaitGroup
	for i := range 20 {
		s := a
		if i%2 == 1 {
			s = b
		}
		wg.Go(func() {
			ok, err := s.Hit(ctx, "api.ip|203.0.113.7", time.Minute, 7, start)
			if err != nil {
				t.Error(err)
			}
			if ok {
				passed.Add(1)
			}
		})
	}
	wg.Wait()
	if n := passed.Load(); n != 7 {
		t.Fatalf("hits allowed = %d, want 7", n)
	}
	// Refused hits were not counted; the next window starts at zero.
	var hits int64
	if err := pool.QueryRow(ctx, "SELECT hits FROM platform.rate_counters").Scan(&hits); err != nil {
		t.Fatal(err)
	}
	if hits != 7 {
		t.Fatalf("stored hits = %d, want 7", hits)
	}
	if ok, err := b.Hit(ctx, "api.ip|203.0.113.7", time.Minute, 7, start.Add(time.Minute)); !ok || err != nil {
		t.Fatalf("next window: %v, %v", ok, err)
	}

	// A claim taken on one replica is refused on the other until it expires.
	exp := start.Add(5 * time.Minute)
	if ok, err := a.Claim(ctx, "pow|abc", exp, start); !ok || err != nil {
		t.Fatalf("first claim: %v, %v", ok, err)
	}
	if ok, err := b.Claim(ctx, "pow|abc", exp, start.Add(time.Second)); ok || err != nil {
		t.Fatalf("replayed claim on the other replica: %v, %v", ok, err)
	}
	if ok, err := b.Claim(ctx, "pow|abc", exp.Add(time.Minute), exp); !ok || err != nil {
		t.Fatalf("claim after expiry: %v, %v", ok, err)
	}

	// Keys are stored hashed only.
	var plain int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM platform.rate_counters WHERE key LIKE '%203.0.113.7%') +
		(SELECT count(*) FROM platform.one_time_claims WHERE key LIKE '%abc%')`).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	if plain != 0 {
		t.Fatal("a raw key reached the database")
	}
}

func TestSweeperDeletesOnlyExpired(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	s, err := ratelimit.NewPostgresStore(pool, hashKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, err := s.Hit(ctx, k, time.Minute, 1, start); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Hit(ctx, "live", time.Hour, 1, start); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(ctx, "old", start.Add(time.Minute), start); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(ctx, "new", start.Add(time.Hour), start); err != nil {
		t.Fatal(err)
	}
	clk := fixedClock(start.Add(2 * time.Minute))
	sw := &ratelimit.Sweeper{Pool: pool, Batch: 2, Clock: clk}
	n, err := sw.SweepOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("deleted %d rows, want 4 (3 counters, 1 claim)", n)
	}
	var counters, claims int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM platform.rate_counters), (SELECT count(*) FROM platform.one_time_claims)").
		Scan(&counters, &claims); err != nil {
		t.Fatal(err)
	}
	if counters != 1 || claims != 1 {
		t.Fatalf("left %d counters and %d claims, want 1 and 1", counters, claims)
	}
	// The live claim still holds.
	if ok, _ := s.Claim(ctx, "new", start.Add(time.Hour), start.Add(2*time.Minute)); ok {
		t.Fatal("sweep freed a live claim")
	}
}

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

func TestNewPostgresStoreNeedsKey(t *testing.T) {
	if _, err := ratelimit.NewPostgresStore(nil, hashKey); err == nil {
		t.Error("nil pool accepted")
	}
}
