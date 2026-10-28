package wiring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	assistantmodel "birge/internal/domains/assistant/model"
	"birge/internal/domains/notify/sms/guard"
	"birge/internal/platform/telemetry"
)

func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

func TestGuardMetricsCountRefusalsAndBudget(t *testing.T) {
	const name = "metrics-test"
	store := newGuardMetrics(nil, name, 3)
	g := guard.New(guard.Config{
		AllowedPrefixes: []string{"+7700"}, PerNumberLimit: 1, GlobalLimit: 100, DailyBudget: 3,
		Store: store, Name: name,
	})
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 23, 0, 0, 0, time.UTC)
	refusedNumber := telemetry.SMSGuardRefusals.WithLabelValues(name, "number_limit")
	refusedBudget := telemetry.SMSGuardRefusals.WithLabelValues(name, "daily_budget")
	counted := telemetry.SMSGuardCounted.WithLabelValues(name)
	base := [3]float64{counterValue(t, refusedNumber), counterValue(t, refusedBudget), counterValue(t, counted)}

	for _, phone := range []string{"+77001111111", "+77002222222", "+77003333333"} {
		if err := g.Allow(ctx, phone, now); err != nil {
			t.Fatalf("Allow(%d) = %v", len(phone), err)
		}
	}
	if err := g.Allow(ctx, "+77001111111", now); !errors.Is(err, guard.ErrNumberLimit) {
		t.Fatalf("repeat = %v, want number limit", err)
	}
	if err := g.Allow(ctx, "+77004444444", now); !errors.Is(err, guard.ErrDailyBudget) {
		t.Fatalf("fourth number = %v, want daily budget", err)
	}
	if got := counterValue(t, refusedNumber) - base[0]; got != 1 {
		t.Errorf("number_limit refusals = %v, want 1", got)
	}
	if got := counterValue(t, refusedBudget) - base[1]; got != 1 {
		t.Errorf("daily_budget refusals = %v, want 1", got)
	}
	if got := counterValue(t, counted) - base[2]; got != 3 {
		t.Errorf("counted = %v, want 3", got)
	}
	if got := store.usedOn(now); got != 3 {
		t.Errorf("used today = %d, want 3", got)
	}
	if got := store.usedOn(now.Add(2 * time.Hour)); got != 0 {
		t.Errorf("used after UTC midnight = %d, want 0", got)
	}
}

func TestGuardCheckNeverReturnsThePhone(t *testing.T) {
	tests := map[string]string{
		"notify.number|+77001234567": "number",
		"notify.global|":             "global",
		"notify.day|":                "day",
		"signin.day|":                "other",
		"notify.+77001234567":        "other",
	}
	for key, want := range tests {
		if got := guardCheck("notify", key); got != want {
			t.Errorf("guardCheck(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestCrisisPathErrorsAreCounted(t *testing.T) {
	lexicon := telemetry.CrisisPathErrors.WithLabelValues("lexicon_unavailable")
	opening := telemetry.CrisisPathErrors.WithLabelValues("escalation_open")
	l0, o0 := counterValue(t, lexicon), counterValue(t, opening)

	if (LexiconCrisis{}).Detect("text", "ru") {
		t.Error("Detect without a lexicon reported a crisis")
	}
	if (&ChatCrisis{}).Detect("text", "ru") {
		t.Error("chat Detect without safety reported a crisis")
	}
	if _, err := (&CrisisOpener{}).OpenEscalation(context.Background(), [16]byte{1}, "checkin"); err == nil {
		t.Error("unconfigured opener returned no error")
	}
	if got := counterValue(t, lexicon) - l0; got != 2 {
		t.Errorf("lexicon_unavailable = %v, want 2", got)
	}
	if got := counterValue(t, opening) - o0; got != 1 {
		t.Errorf("escalation_open = %v, want 1", got)
	}
	// A cancelled request is not a crisis path failure.
	crisisPathError("escalation_open", context.Canceled)
	if got := counterValue(t, opening) - o0; got != 1 {
		t.Errorf("escalation_open after a cancel = %v, want 1", got)
	}
}

type fakeLLM struct{ err error }

func (f fakeLLM) Complete(context.Context, assistantmodel.LLMRequest) (assistantmodel.Completion, error) {
	return assistantmodel.Completion{}, f.err
}

func TestCountingLLM(t *testing.T) {
	ok, failed := telemetry.LLMRequests.WithLabelValues("ok"), telemetry.LLMRequests.WithLabelValues("error")
	ok0, failed0 := counterValue(t, ok), counterValue(t, failed)
	ctx := context.Background()
	_, _ = countingLLM{inner: fakeLLM{}}.Complete(ctx, assistantmodel.LLMRequest{})
	_, _ = countingLLM{inner: fakeLLM{err: errors.New("503")}}.Complete(ctx, assistantmodel.LLMRequest{})
	_, _ = countingLLM{inner: fakeLLM{err: context.Canceled}}.Complete(ctx, assistantmodel.LLMRequest{})
	if counterValue(t, ok)-ok0 != 1 || counterValue(t, failed)-failed0 != 1 {
		t.Errorf("ok +%v, error +%v; want +1 each", counterValue(t, ok)-ok0, counterValue(t, failed)-failed0)
	}
}
