package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/port"
	"birge/internal/platform/clock"
	"birge/internal/platform/outbox"
)

// activeDayCacheMax caps the per-process cache of members already recorded
// today. The cache holds only the current day and is emptied when the day
// changes, so its size is the number of members active today in this
// process; the cap only guards against an unexpected flood. Clearing it when
// full costs one extra conditional UPDATE per member that matches no row, and
// can never emit twice.
const activeDayCacheMax = 100_000

// activeDayTimeout bounds the database work of one recording, so a slow
// database adds at most this much to the request that triggered it.
const activeDayTimeout = 2 * time.Second

// memberActiveDay is the payload of contracts/events/membership.MemberActiveDay.v1.json.
type memberActiveDay struct {
	MemberID uuid.UUID `json:"member_id"`
	Day      string    `json:"day"`
}

// ActiveDays records that a member used the app on a calendar day of the
// deployment time zone and publishes membership.MemberActiveDay at most once
// per member and day (ADR-0024 addendum 2026-10-28). It is called by the
// session middleware for every authenticated member request, so the hot path
// is an in-memory check; only the first request of a member on a day in this
// process reaches the database.
//
// Exactly once across requests and replicas comes from the database, not the
// cache: the conditional update of last_active_day (port.Tx.MarkActiveDay)
// succeeds for one transaction per member and day, and the event is written
// to the outbox in that same transaction only when it did.
//
// Recording is best effort for metrics and never fails the request: errors
// are logged with the event name and member ID only, and the next request
// tries again because a failure is not cached. Safe for concurrent use.
type ActiveDays struct {
	store  port.Store
	clock  clock.Clock
	loc    *time.Location
	logger *slog.Logger

	mu   sync.Mutex
	day  time.Time              // the calendar day seen belongs to (UTC midnight)
	seen map[uuid.UUID]struct{} // members already recorded on day
}

// NewActiveDays returns an ActiveDays for the deployment time zone loc.
// store, clk and loc are required; a nil logger uses slog.Default.
func NewActiveDays(store port.Store, clk clock.Clock, loc *time.Location, logger *slog.Logger) *ActiveDays {
	if store == nil || clk == nil || loc == nil {
		panic("membership: active days need a store, a clock and a time zone")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ActiveDays{store: store, clock: clk, loc: loc, logger: logger, seen: map[uuid.UUID]struct{}{}}
}

// Record notes that memberID used the app now. It returns nothing on purpose:
// the caller must continue the request whatever happens here.
func (a *ActiveDays) Record(ctx context.Context, memberID uuid.UUID) {
	if memberID == uuid.Nil {
		return
	}
	now := a.clock.Now()
	day := calendarDay(now, a.loc)
	if a.cached(memberID, day) {
		return
	}
	// The request may be cancelled right after; the recording still gets its
	// own short deadline instead of the request's.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), activeDayTimeout)
	defer cancel()
	err := a.store.InTx(ctx, func(tx port.Tx) error {
		updated, err := tx.MarkActiveDay(ctx, memberID, day)
		if err != nil || !updated {
			return err
		}
		return tx.Publish(ctx, now.UTC(), outbox.Event{
			Type: model.EventMemberActiveDay, Version: 1,
			Data: memberActiveDay{MemberID: memberID, Day: day.Format(time.DateOnly)},
		})
	})
	if err != nil {
		a.logger.Warn("record active day failed", "event", model.EventMemberActiveDay,
			"context", model.BoundedContext, "member_id", memberID, "error", err)
		return
	}
	// Remembered also when nothing was updated: the day is already recorded,
	// by an earlier process or another replica.
	a.remember(memberID, day)
}

func (a *ActiveDays) cached(memberID uuid.UUID, day time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.day.Equal(day) {
		return false
	}
	_, ok := a.seen[memberID]
	return ok
}

func (a *ActiveDays) remember(memberID uuid.UUID, day time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case day.Before(a.day):
		// A late request from before midnight: keep today's entries.
		return
	case day.After(a.day):
		a.day = day
		clear(a.seen)
	}
	if len(a.seen) >= activeDayCacheMax {
		clear(a.seen)
	}
	a.seen[memberID] = struct{}{}
}

// calendarDay returns the date of t in loc as midnight UTC, so days compare
// with Equal and format as YYYY-MM-DD whatever the zone.
func calendarDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
