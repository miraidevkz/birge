package ratelimit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/platform/clock"
	"birge/internal/platform/store"
)

// minHashKeyLen is the shortest HMAC key PostgresStore accepts.
const minHashKeyLen = 32

// PostgresStore is a Store in PostgreSQL (platform.rate_counters and
// platform.one_time_claims) shared by every replica. Each Hit and Claim is
// one atomic INSERT ... ON CONFLICT statement on the pool, outside any
// caller transaction, so a rolled-back request still counts.
//
// Keys are stored as HMAC-SHA-256(hashKey, key), truncated to 128 bits: the
// tables never hold an IP or a phone number, and without the key a stored
// value cannot be tested against a guessed number.
type PostgresStore struct {
	q       *store.Queries
	hashKey []byte
}

var _ Store = (*PostgresStore)(nil)

// NewPostgresStore returns a store on pool whose keys are hashed with
// hashKey (BIRGE_HASH_KEY_FILE, at least 32 bytes).
func NewPostgresStore(pool *pgxpool.Pool, hashKey []byte) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("ratelimit: postgres store needs a pool")
	}
	if len(hashKey) < minHashKeyLen {
		return nil, fmt.Errorf("ratelimit: postgres store needs a hash key of at least %d bytes", minHashKeyLen)
	}
	return &PostgresStore{q: store.New(pool), hashKey: hashKey}, nil
}

// Hit implements Store with one upsert: the first hit of a window inserts
// the row, later hits add one while the row holds fewer than limit hits.
func (p *PostgresStore) Hit(ctx context.Context, key string, window time.Duration, limit int, now time.Time) (bool, error) {
	if window <= 0 {
		return false, errors.New("ratelimit: window must be positive")
	}
	maxHits := int64(math.MaxInt64)
	if limit > 0 {
		maxHits = int64(limit)
	}
	start := WindowStart(now, window)
	n, err := p.q.HitRateCounter(ctx, store.HitRateCounterParams{
		Key: p.hash(key), WindowStart: start, ExpiresAt: start.Add(window), MaxHits: maxHits,
	})
	if err != nil {
		return false, fmt.Errorf("ratelimit: count hit: %w", err)
	}
	return n == 1, nil
}

// Claim implements Store with one upsert that succeeds only for a new key
// or over an expired claim.
func (p *PostgresStore) Claim(ctx context.Context, key string, expires, now time.Time) (bool, error) {
	n, err := p.q.ClaimOnce(ctx, store.ClaimOnceParams{Key: p.hash(key), ExpiresAt: expires.UTC(), Now: now.UTC()})
	if err != nil {
		return false, fmt.Errorf("ratelimit: claim: %w", err)
	}
	return n == 1, nil
}

func (p *PostgresStore) hash(key string) string {
	m := hmac.New(sha256.New, p.hashKey)
	m.Write([]byte("birge.ratelimit.v1|"))
	m.Write([]byte(key))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// Sweeper defaults.
const (
	DefaultSweepInterval = time.Minute
	DefaultSweepBatch    = 1000
)

// Sweeper deletes expired rows of PostgresStore's tables. Expired rows are
// never read as live (a counter's key includes its window; a claim checks
// its expiry), so the sweep only bounds the tables' size. It runs in the
// worker as a plain loop rather than as a platform timer: it changes no
// domain state, a timer every minute would leave a done row in
// platform.timers each time, and several worker replicas can sweep at once
// (SKIP LOCKED). If no worker runs, the tables only grow.
type Sweeper struct {
	Pool     *pgxpool.Pool
	Interval time.Duration // default DefaultSweepInterval
	Batch    int32         // rows per statement, default DefaultSweepBatch
	Clock    clock.Clock
	Logger   *slog.Logger
}

// Run sweeps every Interval until ctx is cancelled. A failed sweep is
// logged and retried at the next tick; it never stops the worker.
func (s *Sweeper) Run(ctx context.Context) error {
	interval := s.Interval
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := s.SweepOnce(ctx); err != nil && ctx.Err() == nil {
			s.logger().Warn("rate limit sweep failed", "event", "RateLimitSweepFailed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// SweepOnce deletes expired counters and claims in batches until a batch
// comes back short, and returns how many rows it deleted.
func (s *Sweeper) SweepOnce(ctx context.Context) (int64, error) {
	if s.Pool == nil {
		return 0, errors.New("ratelimit: sweeper needs a pool")
	}
	batch := s.Batch
	if batch <= 0 {
		batch = DefaultSweepBatch
	}
	clk := s.Clock
	if clk == nil {
		clk = clock.System{}
	}
	q := store.New(s.Pool)
	now := clk.Now().UTC()
	var total int64
	for _, del := range []func() (int64, error){
		func() (int64, error) {
			return q.DeleteExpiredRateCounters(ctx, store.DeleteExpiredRateCountersParams{Now: now, Batch: batch})
		},
		func() (int64, error) {
			return q.DeleteExpiredClaims(ctx, store.DeleteExpiredClaimsParams{Now: now, Batch: batch})
		},
	} {
		for {
			n, err := del()
			total += n
			if err != nil {
				return total, fmt.Errorf("ratelimit: sweep: %w", err)
			}
			if n < int64(batch) {
				break
			}
		}
	}
	return total, nil
}

func (s *Sweeper) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
