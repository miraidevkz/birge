package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/privacy/model"
	"birge/internal/domains/privacy/port"
	"birge/internal/domains/privacy/privacytest"
	"birge/internal/domains/privacy/service"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func defaultSettings() service.Settings {
	return service.Settings{
		CurrentVersions: map[string]string{
			model.ConsentDataProcessing: "2026-10-01",
			model.ConsentAICrossborder:  "2026-10-01",
			model.ConsentResearch:       "2026-10-01",
		},
		DeletionDueAfter: 24 * time.Hour,
	}
}

func newService(t *testing.T, clk *fixedClock) (*service.Service, *privacytest.MemoryStore, *privacytest.FakeShredder) {
	t.Helper()
	store := privacytest.NewMemoryStore()
	shredder := &privacytest.FakeShredder{}
	svc := &service.Service{
		Store:    store,
		Shredder: shredder,
		Clock:    clk,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: defaultSettings(),
	}
	return svc, store, shredder
}

func TestSetConsentAppendOnlyAndHasConsent(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	svc, store, _ := newService(t, clk)
	member := uuid.Must(uuid.NewV7())
	ctx := context.Background()

	ok, err := svc.HasConsent(ctx, member, model.ConsentDataProcessing)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no consent before grant")
	}

	state, err := svc.SetConsent(ctx, member, model.ConsentDataProcessing, "2026-10-01", true)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Granted || state.CurrentVersion != "2026-10-01" {
		t.Fatalf("state = %+v", state)
	}
	ok, err = svc.HasConsent(ctx, member, model.ConsentDataProcessing)
	if err != nil || !ok {
		t.Fatalf("HasConsent after grant: %v %v", ok, err)
	}
	if store.ConsentCount(member, model.ConsentDataProcessing) != 1 {
		t.Fatal("expected one history row")
	}
	if store.EventCount(model.EventConsentChanged) != 1 {
		t.Fatal("expected one ConsentChanged event")
	}

	clk.t = start.Add(time.Minute)
	if _, err := svc.SetConsent(ctx, member, model.ConsentDataProcessing, "2026-10-01", false); err != nil {
		t.Fatal(err)
	}
	ok, err = svc.HasConsent(ctx, member, model.ConsentDataProcessing)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("revoke must make HasConsent false")
	}
	if store.ConsentCount(member, model.ConsentDataProcessing) != 2 {
		t.Fatalf("history must append, got %d rows", store.ConsentCount(member, model.ConsentDataProcessing))
	}
}

func TestHasConsentFalseWhenVersionChanges(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc, _, _ := newService(t, clk)
	member := uuid.Must(uuid.NewV7())
	ctx := context.Background()

	if _, err := svc.SetConsent(ctx, member, model.ConsentAICrossborder, "2026-10-01", true); err != nil {
		t.Fatal(err)
	}
	svc.Settings.CurrentVersions[model.ConsentAICrossborder] = "2026-11-01"
	ok, err := svc.HasConsent(ctx, member, model.ConsentAICrossborder)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("old version grant must not satisfy current version")
	}

	list, err := svc.ListConsents(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, st := range list {
		if st.Type == model.ConsentAICrossborder {
			found = true
			if st.Granted {
				t.Fatal("ListConsents.granted must be false for stale version")
			}
			if st.CurrentVersion != "2026-11-01" {
				t.Fatalf("currentVersion = %s", st.CurrentVersion)
			}
			if st.GrantedVersion == nil || *st.GrantedVersion != "2026-10-01" {
				t.Fatalf("grantedVersion = %v", st.GrantedVersion)
			}
		}
	}
	if !found {
		t.Fatal("ai_crossborder missing from list")
	}
}

func TestSetConsentRejectsUnknownTypeAndWrongVersion(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc, store, _ := newService(t, clk)
	member := uuid.Must(uuid.NewV7())
	ctx := context.Background()

	_, err := svc.SetConsent(ctx, member, "unknown", "2026-10-01", true)
	if !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("unknown type: %v", err)
	}
	_, err = svc.SetConsent(ctx, member, model.ConsentResearch, "old", true)
	if !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("wrong version: %v", err)
	}
	if store.ConsentCount(member, model.ConsentResearch) != 0 {
		t.Fatal("rejected change must not append")
	}
}

func TestRequestDeletionIdempotent(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	svc, store, _ := newService(t, clk)
	member := uuid.Must(uuid.NewV7())
	ctx := context.Background()

	first, err := svc.RequestDeletion(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	wantDue := start.Add(24 * time.Hour)
	if !first.DueAt.Equal(wantDue) {
		t.Fatalf("due_at = %v, want %v", first.DueAt, wantDue)
	}

	clk.t = start.Add(time.Hour)
	second, err := svc.RequestDeletion(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if !second.RequestedAt.Equal(first.RequestedAt) || !second.DueAt.Equal(first.DueAt) {
		t.Fatalf("second = %+v, first = %+v", second, first)
	}
	if store.EventCount(model.EventDataDeletionRequested) != 1 {
		t.Fatalf("events = %d, want 1", store.EventCount(model.EventDataDeletionRequested))
	}
	due, ok := store.TimerDue(model.TimerDeletionDue, member)
	if !ok || !due.Equal(wantDue) {
		t.Fatalf("timer due = %v ok=%v", due, ok)
	}
}

func TestHandleDeletionDueShredsOnce(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	svc, store, shredder := newService(t, clk)
	member := uuid.Must(uuid.NewV7())
	ctx := context.Background()

	if _, err := svc.RequestDeletion(ctx, member); err != nil {
		t.Fatal(err)
	}
	clk.t = start.Add(24 * time.Hour)
	if err := svc.HandleDeletionDue(ctx, member); err != nil {
		t.Fatal(err)
	}
	if shredder.Len() != 1 {
		t.Fatalf("shred calls = %d, want 1", shredder.Len())
	}
	var completed *time.Time
	if err := store.InTx(ctx, func(tx port.Tx) error {
		req, err := tx.GetDeletionRequest(ctx, member)
		if err != nil {
			return err
		}
		completed = req.CompletedAt
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if completed == nil {
		t.Fatal("expected completed_at set")
	}

	if err := svc.HandleDeletionDue(ctx, member); err != nil {
		t.Fatal(err)
	}
	if shredder.Len() != 1 {
		t.Fatalf("retry must not shred again, got %d", shredder.Len())
	}
}

func TestUnauthorizedWithoutMember(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc, _, _ := newService(t, clk)
	ctx := context.Background()

	if _, err := svc.ListConsents(ctx, uuid.Nil); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("ListConsents: %v", err)
	}
	if _, err := svc.SetConsent(ctx, uuid.Nil, model.ConsentResearch, "2026-10-01", true); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("SetConsent: %v", err)
	}
	if _, err := svc.RequestDeletion(ctx, uuid.Nil); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("RequestDeletion: %v", err)
	}
}
