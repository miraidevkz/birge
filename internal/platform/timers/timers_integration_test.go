package timers_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/platform/db"
	"birge/internal/platform/store"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func TestWorkerFiresRetriesAndCancels(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	clk := &fixedClock{t: time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)}
	subject := uuid.New()

	schedule := func(tm timers.Timer) bool {
		t.Helper()
		var ok bool
		if err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
			var err error
			ok, err = timers.Schedule(ctx, store.New(tx), tm)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return ok
	}

	if !schedule(timers.Timer{Kind: "test.Backup", SubjectID: subject, DedupKey: "esc-1", DueAt: clk.t.Add(15 * time.Minute)}) {
		t.Fatal("first schedule refused")
	}
	if schedule(timers.Timer{Kind: "test.Backup", SubjectID: subject, DedupKey: "esc-1", DueAt: clk.t.Add(15 * time.Minute)}) {
		t.Fatal("duplicate dedup key accepted")
	}

	calls := 0
	fail := true
	w := &timers.Worker{
		Pool: pool, Clock: clk, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Interval: time.Second,
		Handlers: map[string]timers.Handler{"test.Backup": func(context.Context, pgx.Tx, timers.Due) error {
			calls++
			if fail {
				return errors.New("provider down")
			}
			return nil
		}},
	}

	if fired, err := w.RunOnce(ctx); fired || err != nil {
		t.Fatalf("not yet due: fired=%v err=%v", fired, err)
	}
	clk.t = clk.t.Add(16 * time.Minute)
	if fired, err := w.RunOnce(ctx); !fired || err == nil {
		t.Fatalf("failing handler: fired=%v err=%v", fired, err)
	}
	if fired, _ := w.RunOnce(ctx); fired {
		t.Fatal("retried before backoff elapsed")
	}
	fail = false
	clk.t = clk.t.Add(timers.Backoff(0) + time.Second)
	if fired, err := w.RunOnce(ctx); !fired || err != nil {
		t.Fatalf("retry: fired=%v err=%v", fired, err)
	}
	if fired, _ := w.RunOnce(ctx); fired {
		t.Fatal("timer fired twice")
	}
	if calls != 2 {
		t.Fatalf("handler calls = %d, want 2", calls)
	}

	// A done timer frees its dedup key; cancellation stops a pending one.
	if !schedule(timers.Timer{Kind: "test.Backup", SubjectID: subject, DedupKey: "esc-1", DueAt: clk.t.Add(time.Minute)}) {
		t.Fatal("dedup key not freed after completion")
	}
	n, err := timers.Cancel(ctx, store.New(pool), clk.t, "test.Backup", subject)
	if err != nil || n != 1 {
		t.Fatalf("Cancel = %d, %v", n, err)
	}
	clk.t = clk.t.Add(time.Hour)
	if fired, _ := w.RunOnce(ctx); fired {
		t.Fatal("cancelled timer fired")
	}
}
