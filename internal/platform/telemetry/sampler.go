package telemetry

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/platform/clock"
	"birge/internal/platform/store"
)

// Sampler defaults.
const (
	defaultSampleInterval = 15 * time.Second
	sampleTimeout         = 5 * time.Second
	// escalationLookback bounds the untaken-escalation query to a range of
	// the outbox primary key (uuidv7, see uuidV7Floor). An escalation
	// untaken for a week has paged on-call every evaluation since its
	// fifteenth minute; past that it drops out of the gauge.
	escalationLookback = 7 * 24 * time.Hour
)

// Sampler refreshes the gauges that come from the database: outbox lag,
// due timers and untaken escalations. It runs in the worker only (one set
// of queries per worker replica, not per API replica); with several
// workers each reports the same values, so alerts aggregate with max().
type Sampler struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Logger   *slog.Logger
	Interval time.Duration // default 15 s
}

// Run samples until ctx is cancelled. A failed sample is logged and the
// gauges keep their last values; birge_sampler_last_success_timestamp_seconds
// stops moving, which has its own alert, so a broken sampler cannot
// silence the crisis alerts unnoticed.
func (s *Sampler) Run(ctx context.Context) error {
	interval := s.Interval
	if interval <= 0 {
		interval = defaultSampleInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.Sample(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Warn("telemetry sample failed", "event", "TelemetrySampleFailed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Sample runs the queries once and sets the gauges. Each gauge is set only
// when its own query succeeded.
func (s *Sampler) Sample(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	now := s.Clock.Now().UTC()
	q := store.New(s.Pool)
	var errs []error

	if oldest, err := q.OutboxLag(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("outbox lag: %w", err))
	} else {
		OutboxOldestAge.Set(age(now, oldest))
	}
	if t, err := q.DueTimers(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("due timers: %w", err))
	} else {
		TimersDue.Set(float64(t.Due))
		TimersOldestOverdue.Set(age(now, t.Oldest))
	}
	since := now.Add(-escalationLookback)
	if e, err := q.UntakenEscalations(ctx, store.UntakenEscalationsParams{Now: now, MinID: uuidV7Floor(since), Since: since}); err != nil {
		errs = append(errs, fmt.Errorf("untaken escalations: %w", err))
	} else {
		EscalationsUntaken.Set(float64(e.Untaken))
		EscalationsOldestUntaken.Set(age(now, e.Oldest))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	SamplerLastSuccess.Set(float64(now.Unix()))
	return nil
}

// uuidV7Floor is the smallest UUIDv7 of the millisecond t: every uuidv7
// generated at or after t sorts at or above it (RFC 9562: the first 48 bits
// are the Unix time in milliseconds, then the version and variant).
func uuidV7Floor(t time.Time) uuid.UUID {
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(max(t.UnixMilli(), 0))) //nolint:gosec // clamped to non-negative
	var u uuid.UUID
	copy(u[:6], ms[2:])
	u[6] = 0x70 // version 7, rand_a zero
	u[8] = 0x80 // variant 10, rand_b zero
	return u
}

// age is now - t in seconds, never negative (clock skew between replicas).
func age(now, t time.Time) float64 {
	return max(now.Sub(t).Seconds(), 0)
}
