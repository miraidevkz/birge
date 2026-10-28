package guard_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"birge/internal/domains/notify/sms/guard"
	"birge/internal/platform/ratelimit"
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
	if err := g.Allow(context.Background(), "+77771112233", now); !errors.Is(err, guard.ErrPrefixRejected) {
		t.Fatalf("Allow = %v, want ErrPrefixRejected", err)
	}
	if err := g.Allow(context.Background(), "+77001112233", now); err != nil {
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
	if err := g.Allow(context.Background(), phone, now); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow(context.Background(), phone, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow(context.Background(), phone, now.Add(2*time.Second)); !errors.Is(err, guard.ErrNumberLimit) {
		t.Fatalf("Allow = %v, want ErrNumberLimit", err)
	}
	// Other numbers still allowed.
	if err := g.Allow(context.Background(), "+77009998877", now.Add(3*time.Second)); err != nil {
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
	if err := g.Allow(context.Background(), "+77001110001", now); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow(context.Background(), "+77001110002", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow(context.Background(), "+77001110003", now.Add(2*time.Second)); !errors.Is(err, guard.ErrGlobalLimit) {
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
	if err := g.Allow(context.Background(), "+77001110001", now); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow(context.Background(), "+77001110002", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow(context.Background(), "+77001110003", now.Add(2*time.Second)); !errors.Is(err, guard.ErrDailyBudget) {
		t.Fatalf("Allow = %v, want ErrDailyBudget", err)
	}
	// Next UTC day resets the budget.
	nextDay := time.Date(2026, 10, 28, 0, 0, 1, 0, time.UTC)
	if err := g.Allow(context.Background(), "+77001110003", nextDay); err != nil {
		t.Fatalf("next day: %v", err)
	}
}

func TestGuardEmptyAllowListRejectsAll(t *testing.T) {
	t.Parallel()
	g := guard.New(guard.Config{DailyBudget: 10})
	if err := g.Allow(context.Background(), "+77001112233", time.Now().UTC()); !errors.Is(err, guard.ErrPrefixRejected) {
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
	if err := g.Allow(context.Background(), "+77001110001", now); err != nil {
		t.Fatal(err)
	}
	if err := g.Allow(context.Background(), "+77001110002", now.Add(time.Second)); !errors.Is(err, guard.ErrDailyBudget) {
		t.Fatalf("Allow = %v, want ErrDailyBudget", err)
	}
	if err := g.AllowCritical(context.Background(), "+77001110002", now.Add(2*time.Second)); err != nil {
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
	if err := g.AllowCritical(context.Background(), "+77771112233", now); !errors.Is(err, guard.ErrPrefixRejected) {
		t.Fatalf("AllowCritical = %v, want ErrPrefixRejected", err)
	}
}

// downStore fails every call, like a shared store whose database is down.
type downStore struct{}

func (downStore) Hit(context.Context, string, time.Duration, int, time.Time) (bool, error) {
	return false, errors.New("database down")
}

func (downStore) Claim(context.Context, string, time.Time, time.Time) (bool, error) {
	return false, errors.New("database down")
}

func TestGuardStoreFailure(t *testing.T) {
	t.Parallel()
	g := guard.New(guard.Config{AllowedPrefixes: []string{"+7"}, PerNumberLimit: 5, GlobalLimit: 10, DailyBudget: 10, Store: downStore{}})
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	if err := g.Allow(context.Background(), "+77001110001", now); !errors.Is(err, guard.ErrUnavailable) {
		t.Fatalf("Allow = %v, want ErrUnavailable (fail closed)", err)
	}
	if err := g.AllowCritical(context.Background(), "+77001110001", now); err != nil {
		t.Fatalf("AllowCritical = %v, want nil (a critical SMS is never blocked by the store)", err)
	}
	if err := g.AllowCritical(context.Background(), "+15550001111", now); !errors.Is(err, guard.ErrPrefixRejected) {
		t.Fatalf("AllowCritical outside the prefixes = %v, want ErrPrefixRejected", err)
	}
}

// Two worker replicas share one store: the per-number limit and the daily
// budget hold across both; guards with another name count separately.
func TestGuardSharedStore(t *testing.T) {
	t.Parallel()
	shared := ratelimit.NewMemoryStore(100)
	cfg := guard.Config{AllowedPrefixes: []string{"+7"}, PerNumberLimit: 1, GlobalLimit: 100, DailyBudget: 2, Store: shared, Name: "notify"}
	a, b := guard.New(cfg), guard.New(cfg)
	cfg.Name = "signin"
	signin := guard.New(cfg)
	ctx := context.Background()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	if err := a.Allow(ctx, "+77001110001", now); err != nil {
		t.Fatal(err)
	}
	if err := b.Allow(ctx, "+77001110001", now); !errors.Is(err, guard.ErrNumberLimit) {
		t.Fatalf("second replica: %v, want ErrNumberLimit", err)
	}
	if err := b.Allow(ctx, "+77001110002", now); err != nil {
		t.Fatal(err)
	}
	if err := a.Allow(ctx, "+77001110003", now); !errors.Is(err, guard.ErrDailyBudget) {
		t.Fatalf("budget across replicas: %v, want ErrDailyBudget", err)
	}
	if err := signin.Allow(ctx, "+77001110001", now); err != nil {
		t.Fatalf("sign-in guard shares counters with notify: %v", err)
	}
}
