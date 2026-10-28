// Package guard rejects SMS that would pump providers: disallowed number
// prefixes, per-number and global rate limits, and a daily budget circuit
// breaker. It never logs phone numbers.
//
// The counters live in a platform/ratelimit Store (fixed windows aligned to
// absolute time; the daily budget is the UTC day). With the PostgreSQL store
// every worker replica shares them, so N workers keep one per-number limit,
// one global limit and one daily budget instead of N of each; the store keeps
// only a keyed hash of the number. Without a store (tests, one replica) a
// guard counts in a private in-memory table.
//
// A store failure refuses an ordinary SMS (fail closed: an unknown count must
// not open the provider to pumping) but never a critical one: the critical
// SMS is the escalation fallback of the crisis path (AGENTS.md,
// non-negotiable 1), and the prefix allow-list still applies to it.
package guard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"birge/internal/platform/ratelimit"
)

var (
	// ErrPrefixRejected means the number's prefix is not in the allow-list.
	ErrPrefixRejected = errors.New("sms/guard: prefix not allowed")
	// ErrNumberLimit means this number exceeded its rate limit.
	ErrNumberLimit = errors.New("sms/guard: number rate limit exceeded")
	// ErrGlobalLimit means the guard exceeded its global rate limit.
	ErrGlobalLimit = errors.New("sms/guard: global rate limit exceeded")
	// ErrDailyBudget means the daily SMS budget is exhausted.
	ErrDailyBudget = errors.New("sms/guard: daily budget exhausted")
	// ErrUnavailable means the counter store failed; an ordinary SMS is refused.
	ErrUnavailable = errors.New("sms/guard: counter store unavailable")
)

// day is the window of the daily budget: one UTC day (ratelimit windows are
// aligned to the Unix epoch, which is midnight UTC).
const day = 24 * time.Hour

// Config controls allow-lists and limits. Zero values disable the corresponding check
// except AllowedPrefixes: an empty list rejects every number.
type Config struct {
	// AllowedPrefixes are E.164 prefixes (e.g. "+7700"). Empty rejects all.
	AllowedPrefixes []string
	// PerNumberLimit is the max sends per number inside PerNumberWindow.
	PerNumberLimit int
	// PerNumberWindow is the fixed window for PerNumberLimit (default 1h).
	PerNumberWindow time.Duration
	// GlobalLimit is the max sends across all numbers inside GlobalWindow.
	GlobalLimit int
	// GlobalWindow is the fixed window for GlobalLimit (default 1m).
	GlobalWindow time.Duration
	// DailyBudget is the max sends per UTC day. Zero means unlimited.
	DailyBudget int
	// Store keeps the counters. Nil gives the guard a private in-memory
	// table (tests, a single worker replica).
	Store ratelimit.Store
	// Name separates this guard's counters from another guard's in a shared
	// store, e.g. "notify" and "signin". Default "sms".
	Name string
	// Logger receives store failures (guard name only). Default slog.Default().
	Logger *slog.Logger
}

// Guard is the anti-pumping gate used before every SMS send. It is safe for
// concurrent use.
type Guard struct {
	cfg Config
}

// New returns a Guard with cfg defaults applied.
func New(cfg Config) *Guard {
	if cfg.PerNumberWindow <= 0 {
		cfg.PerNumberWindow = time.Hour
	}
	if cfg.GlobalWindow <= 0 {
		cfg.GlobalWindow = time.Minute
	}
	if cfg.Store == nil {
		cfg.Store = ratelimit.NewMemoryStore(0)
	}
	if cfg.Name == "" {
		cfg.Name = "sms"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Guard{cfg: cfg}
}

// Allow reports whether an SMS to phoneE164 may be sent at now.
// On success it records the send against the limits. phoneE164 is never logged.
func (g *Guard) Allow(ctx context.Context, phoneE164 string, now time.Time) error {
	return g.allow(ctx, phoneE164, now, false)
}

// AllowCritical is Allow for class=critical SMS: prefix allow-list and per-number
// and global limits still apply, but the daily budget breaker does not block
// the send (it is still counted), and a store failure lets it through.
func (g *Guard) AllowCritical(ctx context.Context, phoneE164 string, now time.Time) error {
	return g.allow(ctx, phoneE164, now, true)
}

// allow counts in a fixed order: per number, global, daily budget. A refused
// count is not recorded, so a number that hits its own limit uses up neither
// the global limit nor the budget (sign-in pumping of one number cannot
// drain them). A refusal at a later step leaves the earlier counts in place,
// which only errs on the side of fewer SMS.
func (g *Guard) allow(ctx context.Context, phoneE164 string, now time.Time, critical bool) error {
	phoneE164 = strings.TrimSpace(phoneE164)
	if phoneE164 == "" || !strings.HasPrefix(phoneE164, "+") {
		return ErrPrefixRejected
	}
	if !g.prefixAllowed(phoneE164) {
		return ErrPrefixRejected
	}
	budget := g.cfg.DailyBudget
	if critical && budget > 0 {
		budget = -1 // count it, never block it
	}
	for _, c := range []struct {
		key    string
		window time.Duration
		limit  int
		refuse error
	}{
		{".number|" + phoneE164, g.cfg.PerNumberWindow, g.cfg.PerNumberLimit, ErrNumberLimit},
		{".global|", g.cfg.GlobalWindow, g.cfg.GlobalLimit, ErrGlobalLimit},
		{".day|", day, budget, ErrDailyBudget},
	} {
		if c.limit == 0 {
			continue // check disabled
		}
		ok, err := g.cfg.Store.Hit(ctx, g.cfg.Name+c.key, c.window, max(c.limit, 0), now)
		if err != nil {
			g.cfg.Logger.Warn("sms guard counter store failed", "event", "SMSGuardStoreError",
				"guard", g.cfg.Name, "critical", critical, "error", err)
			if critical {
				continue
			}
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		if !ok {
			return c.refuse
		}
	}
	return nil
}

func (g *Guard) prefixAllowed(phone string) bool {
	if len(g.cfg.AllowedPrefixes) == 0 {
		return false
	}
	for _, p := range g.cfg.AllowedPrefixes {
		if p != "" && strings.HasPrefix(phone, p) {
			return true
		}
	}
	return false
}

// Explain returns err's message; callers use it so wrapped errors stay free of phone numbers.
func Explain(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
