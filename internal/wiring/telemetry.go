package wiring

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	assistantmodel "birge/internal/domains/assistant/model"
	assistantport "birge/internal/domains/assistant/port"
	"birge/internal/platform/ratelimit"
	"birge/internal/platform/telemetry"
)

// Metrics that wiring records at the seams between contexts (ADR-0021): the
// SMS guard's counter store, the crisis adapters and the LLM client. The
// contexts themselves stay free of telemetry code.

// guardMetrics wraps the counter store of one SMS guard and turns its hits
// into metrics: refusals by reason, store failures, and the sends counted
// against the daily budget. The guard keys its counters
// "<guard>.number|<phone>", "<guard>.global|" and "<guard>.day|"
// (notify/sms/guard); only the part between the guard name and "|" is
// read, so the phone never reaches a label.
type guardMetrics struct {
	ratelimit.Store
	guard string

	mu   sync.Mutex
	day  time.Time // UTC day that used belongs to
	used int
}

// newGuardMetrics wraps store for the guard called name and reports its
// daily count. A nil store becomes the private memory store the guard
// would otherwise create for itself.
func newGuardMetrics(store ratelimit.Store, name string, dailyBudget int) *guardMetrics {
	if store == nil {
		store = ratelimit.NewMemoryStore(0)
	}
	g := &guardMetrics{Store: store, guard: name}
	telemetry.SMSGuardDailyBudget.WithLabelValues(name).Set(float64(dailyBudget))
	telemetry.SetSMSDailyUsed(name, func() float64 { return float64(g.usedOn(time.Now())) })
	return g
}

// Hit implements ratelimit.Store and records the outcome.
func (g *guardMetrics) Hit(ctx context.Context, key string, window time.Duration, limit int, now time.Time) (bool, error) {
	ok, err := g.Store.Hit(ctx, key, window, limit, now)
	check := guardCheck(g.guard, key)
	switch {
	case err != nil:
		telemetry.SMSGuardStoreErrors.WithLabelValues(g.guard).Inc()
	case !ok:
		telemetry.SMSGuardRefusals.WithLabelValues(g.guard, refusalReason(check)).Inc()
	case check == "day":
		// The budget is the last check: a counted day hit is a send.
		telemetry.SMSGuardCounted.WithLabelValues(g.guard).Inc()
		g.count(now)
	}
	return ok, err //nolint:wrapcheck // a decorator passes the store's error through unchanged
}

func (g *guardMetrics) count(now time.Time) {
	day := now.UTC().Truncate(24 * time.Hour)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !day.Equal(g.day) {
		g.day, g.used = day, 0
	}
	g.used++
}

// usedOn is the count of the UTC day of now (0 once the day has changed).
func (g *guardMetrics) usedOn(now time.Time) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !now.UTC().Truncate(24 * time.Hour).Equal(g.day) {
		return 0
	}
	return g.used
}

// guardCheck extracts the check name ("number", "global", "day") from a
// guard counter key, or "other".
func guardCheck(guard, key string) string {
	rest, ok := strings.CutPrefix(key, guard+".")
	if !ok {
		return "other"
	}
	check, _, ok := strings.Cut(rest, "|")
	if !ok {
		return "other"
	}
	switch check {
	case "number", "global", "day":
		return check
	}
	return "other"
}

func refusalReason(check string) string {
	switch check {
	case "number":
		return "number_limit"
	case "global":
		return "global_limit"
	case "day":
		return "daily_budget"
	}
	return "other"
}

// crisisPathError counts a failure on the crisis path. A cancelled context
// (the member closed the app) is not a failure of ours.
func crisisPathError(stage string, err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	telemetry.CrisisPathErrors.WithLabelValues(stage).Inc()
}

// errLexiconUnavailable marks a crisis check that ran without a lexicon.
var errLexiconUnavailable = errors.New("wiring: crisis lexicon unavailable")

// countingLLM counts assistant LLM calls by outcome for the LLM error-rate
// alert. A cancelled call (the member left) is not counted.
type countingLLM struct {
	inner assistantport.LLM
}

var _ assistantport.LLM = countingLLM{}

// Complete calls the LLM and records ok or error.
func (c countingLLM) Complete(ctx context.Context, req assistantmodel.LLMRequest) (assistantmodel.Completion, error) {
	out, err := c.inner.Complete(ctx, req)
	switch {
	case err == nil:
		telemetry.LLMRequests.WithLabelValues("ok").Inc()
	case !errors.Is(err, context.Canceled):
		telemetry.LLMRequests.WithLabelValues("error").Inc()
	}
	return out, err //nolint:wrapcheck // a decorator passes the client's error through unchanged
}
