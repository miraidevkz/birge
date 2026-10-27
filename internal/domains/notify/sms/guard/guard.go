// Package guard rejects SMS that would pump providers: disallowed number
// prefixes, per-number and global rate limits, and a daily budget circuit breaker.
// It never logs phone numbers.
package guard

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	// ErrPrefixRejected means the number's prefix is not in the allow-list.
	ErrPrefixRejected = errors.New("sms/guard: prefix not allowed")
	// ErrNumberLimit means this number exceeded its rate limit.
	ErrNumberLimit = errors.New("sms/guard: number rate limit exceeded")
	// ErrGlobalLimit means the process exceeded its global rate limit.
	ErrGlobalLimit = errors.New("sms/guard: global rate limit exceeded")
	// ErrDailyBudget means the daily SMS budget is exhausted.
	ErrDailyBudget = errors.New("sms/guard: daily budget exhausted")
)

// Config controls allow-lists and limits. Zero values disable the corresponding check
// except AllowedPrefixes: an empty list rejects every number.
type Config struct {
	// AllowedPrefixes are E.164 prefixes (e.g. "+7700"). Empty rejects all.
	AllowedPrefixes []string
	// PerNumberLimit is the max sends per number inside PerNumberWindow.
	PerNumberLimit int
	// PerNumberWindow is the sliding window for PerNumberLimit (default 1h).
	PerNumberWindow time.Duration
	// GlobalLimit is the max sends across all numbers inside GlobalWindow.
	GlobalLimit int
	// GlobalWindow is the sliding window for GlobalLimit (default 1m).
	GlobalWindow time.Duration
	// DailyBudget is the max sends per UTC day. Zero means unlimited.
	DailyBudget int
}

// Guard is a process-local anti-pumping gate used before every SMS send.
type Guard struct {
	cfg Config

	mu       sync.Mutex
	numbers  map[string][]time.Time
	global   []time.Time
	day      string // YYYY-MM-DD UTC
	dayCount int
}

// New returns a Guard with cfg defaults applied.
func New(cfg Config) *Guard {
	if cfg.PerNumberWindow <= 0 {
		cfg.PerNumberWindow = time.Hour
	}
	if cfg.GlobalWindow <= 0 {
		cfg.GlobalWindow = time.Minute
	}
	return &Guard{
		cfg:     cfg,
		numbers: make(map[string][]time.Time),
	}
}

// Allow reports whether an SMS to phoneE164 may be sent at now.
// On success it records the send against the limits. phoneE164 is never logged.
func (g *Guard) Allow(phoneE164 string, now time.Time) error {
	return g.allow(phoneE164, now, false)
}

// AllowCritical is Allow for class=critical SMS: prefix allow-list and per-number
// limit still apply, but the daily budget breaker does not block the send.
func (g *Guard) AllowCritical(phoneE164 string, now time.Time) error {
	return g.allow(phoneE164, now, true)
}

func (g *Guard) allow(phoneE164 string, now time.Time, critical bool) error {
	phoneE164 = strings.TrimSpace(phoneE164)
	if phoneE164 == "" || !strings.HasPrefix(phoneE164, "+") {
		return ErrPrefixRejected
	}
	if !g.prefixAllowed(phoneE164) {
		return ErrPrefixRejected
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	now = now.UTC()
	day := now.Format("2006-01-02")
	if g.day != day {
		g.day = day
		g.dayCount = 0
	}
	if !critical && g.cfg.DailyBudget > 0 && g.dayCount >= g.cfg.DailyBudget {
		return ErrDailyBudget
	}

	g.global = prune(g.global, now.Add(-g.cfg.GlobalWindow))
	if g.cfg.GlobalLimit > 0 && len(g.global) >= g.cfg.GlobalLimit {
		return ErrGlobalLimit
	}

	per := prune(g.numbers[phoneE164], now.Add(-g.cfg.PerNumberWindow))
	if g.cfg.PerNumberLimit > 0 && len(per) >= g.cfg.PerNumberLimit {
		g.numbers[phoneE164] = per
		return ErrNumberLimit
	}

	per = append(per, now)
	g.numbers[phoneE164] = per
	g.global = append(g.global, now)
	g.dayCount++
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

func prune(in []time.Time, cutoff time.Time) []time.Time {
	if len(in) == 0 {
		return in
	}
	i := 0
	for i < len(in) && !in[i].After(cutoff) {
		i++
	}
	if i == 0 {
		return in
	}
	out := make([]time.Time, len(in)-i)
	copy(out, in[i:])
	return out
}

// Explain returns err's message; callers use it so wrapped errors stay free of phone numbers.
func Explain(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
