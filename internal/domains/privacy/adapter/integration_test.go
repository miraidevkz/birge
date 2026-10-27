package adapter_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/privacy/adapter"
	"birge/internal/domains/privacy/model"
	"birge/internal/domains/privacy/privacytest"
	"birge/internal/domains/privacy/service"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func TestConsentHistoryAppendOnlyPostgres(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	store := &adapter.PostgresStore{Pool: pool}
	member := uuid.Must(uuid.NewV7())
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := &service.Service{
		Store:  store,
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service.Settings{
			CurrentVersions: map[string]string{model.ConsentDataProcessing: "v1"},
		},
	}

	if _, err := svc.SetConsent(ctx, member, model.ConsentDataProcessing, "v1", true); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(time.Minute)
	if _, err := svc.SetConsent(ctx, member, model.ConsentDataProcessing, "v1", false); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM privacy.consent_history WHERE member_id = $1 AND type = $2`,
		member, model.ConsentDataProcessing,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("history rows = %d, want 2", count)
	}

	ok, err := svc.HasConsent(ctx, member, model.ConsentDataProcessing)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("revoked consent must be false")
	}

	var events int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM platform.outbox WHERE event_type = $1`,
		model.EventConsentChanged,
	).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("outbox events = %d, want 2", events)
	}
}

func TestDeletionRequestIdempotentAndTimerShreds(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	store := &adapter.PostgresStore{Pool: pool}
	member := uuid.Must(uuid.NewV7())
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	shredder := &privacytest.FakeShredder{}
	handlers := &adapter.TimerHandlers{}
	svc := &service.Service{
		Store:    store,
		Shredder: shredder,
		Clock:    clk,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service.Settings{
			CurrentVersions:  map[string]string{model.ConsentDataProcessing: "v1"},
			DeletionDueAfter: 24 * time.Hour,
		},
	}
	handlers.Service = svc

	first, err := svc.RequestDeletion(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.RequestDeletion(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if !first.DueAt.Equal(second.DueAt) {
		t.Fatalf("due mismatch: %v vs %v", first.DueAt, second.DueAt)
	}

	var reqCount, eventCount, timerCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM privacy.deletion_requests WHERE member_id = $1`, member).Scan(&reqCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform.outbox WHERE event_type = $1`, model.EventDataDeletionRequested).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM platform.timers WHERE kind = $1 AND subject_id = $2 AND done_at IS NULL`,
		model.TimerDeletionDue, member,
	).Scan(&timerCount); err != nil {
		t.Fatal(err)
	}
	if reqCount != 1 || eventCount != 1 || timerCount != 1 {
		t.Fatalf("req=%d event=%d timer=%d, want 1 each", reqCount, eventCount, timerCount)
	}

	clk.t = first.DueAt
	worker := timers.Worker{
		Pool:     pool,
		Clock:    clk,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: handlers.Handlers(),
	}
	fired, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("expected deletion timer to fire")
	}
	if shredder.Len() != 1 {
		t.Fatalf("shred calls = %d, want 1", shredder.Len())
	}

	var completed bool
	if err := pool.QueryRow(ctx,
		`SELECT completed_at IS NOT NULL FROM privacy.deletion_requests WHERE member_id = $1`,
		member,
	).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("expected completed_at")
	}

	// Timer already done; a second RunOnce must not shred again.
	fired, err = worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fired {
		t.Fatal("no further due timers expected")
	}
	if shredder.Len() != 1 {
		t.Fatalf("shred calls after idle = %d, want 1", shredder.Len())
	}
}
