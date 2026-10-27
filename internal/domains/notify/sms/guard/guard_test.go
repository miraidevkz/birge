package guard_test

import (
	"errors"
	"testing"
	"time"

	"birge/internal/domains/notify/sms/guard"
)

func TestGuardRejectsDisallowedPrefix(t *testing.T) {
	t.Parallel()
	g := guard.New(guard.Config{
		AllowedPrefixes: []string{"+7700"},
		PerNumberLimit:  10,
		GlobalLimit:     100,
		DailyBudget:     100,
	})
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	if err := g.Allow("+77771112233", now); !errors.Is(err, guard.ErrPrefixRejected) {
		t.Fatalf("Allow = %v, want ErrPrefixRejected", err)
	}
	if err := g.Allow("+77001112233", now); err != nil {
		t.Fatalf("allowed prefix: %v", err)
	}
}

func TestGuardPerNumberLimit(t *testing.T) {
	t.Parallel()
	g := guard.New(guard.Config{
		AllowedPrefixes: []string{"+7"},
		PerNumberLimit:  2,
		PerNumberWindow: time.Hour,
		GlobalLimit:     100,
		DailyBudget:     100,
	})
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	phone := "+77001112233"
	if err := g.Allow(phone, now); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow(phone, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow(phone, now.Add(2*time.Second)); !errors.Is(err, guard.ErrNumberLimit) {
		t.Fatalf("Allow = %v, want ErrNumberLimit", err)
	}
	// Other numbers still allowed.
	if err := g.Allow("+77009998877", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestGuardGlobalLimit(t *testing.T) {
	t.Parallel()
	g := guard.New(guard.Config{
		AllowedPrefixes: []string{"+7"},
		PerNumberLimit:  10,
		GlobalLimit:     2,
		GlobalWindow:    time.Minute,
		DailyBudget:     100,
	})
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	if err := g.Allow("+77001110001", now); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow("+77001110002", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow("+77001110003", now.Add(2*time.Second)); !errors.Is(err, guard.ErrGlobalLimit) {
		t.Fatalf("Allow = %v, want ErrGlobalLimit", err)
	}
}

func TestGuardDailyBudget(t *testing.T) {
	t.Parallel()
	g := guard.New(guard.Config{
		AllowedPrefixes: []string{"+7"},
		PerNumberLimit:  100,
		GlobalLimit:     100,
		DailyBudget:     2,
	})
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	if err := g.Allow("+77001110001", now); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow("+77001110002", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow("+77001110003", now.Add(2*time.Second)); !errors.Is(err, guard.ErrDailyBudget) {
		t.Fatalf("Allow = %v, want ErrDailyBudget", err)
	}
	// Next UTC day resets the budget.
	nextDay := time.Date(2026, 10, 28, 0, 0, 1, 0, time.UTC)
	if err := g.Allow("+77001110003", nextDay); err != nil {
		t.Fatalf("next day: %v", err)
	}
}

func TestGuardEmptyAllowListRejectsAll(t *testing.T) {
	t.Parallel()
	g := guard.New(guard.Config{DailyBudget: 10})
	if err := g.Allow("+77001112233", time.Now().UTC()); !errors.Is(err, guard.ErrPrefixRejected) {
		t.Fatalf("Allow = %v, want ErrPrefixRejected", err)
	}
}

func TestGuardAllowCriticalIgnoresDailyBudget(t *testing.T) {
	t.Parallel()
	g := guard.New(guard.Config{
		AllowedPrefixes: []string{"+7"},
		PerNumberLimit:  10,
		GlobalLimit:     100,
		DailyBudget:     1,
	})
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	if err := g.Allow("+77001110001", now); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow("+77001110002", now.Add(time.Second)); !errors.Is(err, guard.ErrDailyBudget) {
		t.Fatalf("Allow = %v, want ErrDailyBudget", err)
	}
	if err := g.AllowCritical("+77001110002", now.Add(2*time.Second)); err != nil {
		t.Fatalf("AllowCritical = %v, want nil", err)
	}
}

func TestGuardAllowCriticalStillEnforcesPrefix(t *testing.T) {
	t.Parallel()
	g := guard.New(guard.Config{
		AllowedPrefixes: []string{"+7700"},
		PerNumberLimit:  10,
		DailyBudget:     0,
	})
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	if err := g.AllowCritical("+77771112233", now); !errors.Is(err, guard.ErrPrefixRejected) {
		t.Fatalf("AllowCritical = %v, want ErrPrefixRejected", err)
	}
}
