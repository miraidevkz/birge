package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/wellbeing/model"
	"birge/internal/domains/wellbeing/port"
	"birge/internal/domains/wellbeing/service"
	"birge/internal/domains/wellbeing/wellbeingtest"
)

func TestCohortAggregates(t *testing.T) {
	ctx := context.Background()
	week := 7 * 24 * time.Hour
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	store := wellbeingtest.NewMemoryStore()
	svc := &service.Service{Store: store, Clock: &fixedClock{t: to.Add(20 * week)}}

	in1, in2, before, after := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	add := func(m uuid.UUID, pss int, at time.Time) {
		store.AddCheckin(model.Checkin{MemberID: m, PSS4: pss, Flag: model.FlagNone, CompletedAt: at})
	}
	// In the cohort: first check-in inside the period; follow-ups after `to` still count.
	add(in1, 12, from.Add(24*time.Hour))
	add(in1, 6, from.Add(24*time.Hour+8*week))
	add(in2, 8, to.Add(-time.Hour))
	add(in2, 4, to.Add(-time.Hour+8*week))
	// Not in the cohort: first check-in before the period, even with one inside it.
	add(before, 16, from.Add(-time.Hour))
	add(before, 16, from.Add(time.Hour))
	// Not in the cohort: first check-in exactly at `to` (half-open period).
	add(after, 0, to)

	got, err := svc.CohortAggregates(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	want := model.CohortAggregates{
		StressBaseline: model.ScoreStats{Count: 2, Mean: 10, Median: 10},
		StressWeek8:    model.ScoreStats{Count: 2, Mean: 5, Median: 5},
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCohortAggregatesRejectsBadPeriod(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := &service.Service{Store: wellbeingtest.NewMemoryStore(), Clock: &fixedClock{t: at}}
	for _, p := range []struct{ from, to time.Time }{
		{at, at},
		{at.Add(time.Hour), at},
		{time.Time{}, at},
		{at, time.Time{}},
	} {
		if _, err := svc.CohortAggregates(context.Background(), p.from, p.to); !errors.Is(err, model.ErrInvalidInput) {
			t.Fatalf("period %v..%v: err = %v, want ErrInvalidInput", p.from, p.to, err)
		}
	}
}

type failingStore struct{ err error }

func (f failingStore) InTx(context.Context, func(port.Tx) error) error { return f.err }

func TestCohortAggregatesStoreError(t *testing.T) {
	boom := errors.New("db down")
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := &service.Service{Store: failingStore{err: boom}, Clock: &fixedClock{t: at}}
	if _, err := svc.CohortAggregates(context.Background(), at, at.Add(time.Hour)); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}
