package telemetry_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	"birge/internal/platform/store"
	"birge/internal/platform/telemetry"
	"birge/internal/platform/testinfra"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetGauge().GetValue()
}

func TestSamplerReadsOutboxTimersAndEscalations(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	taken, open, closed := uuid.New(), uuid.New(), uuid.New()

	events := []struct {
		at   time.Time
		e    outbox.Event
		sent bool
	}{
		{now.Add(-40 * time.Minute), outbox.Event{Type: "safety.EscalationOpened", Version: 1, Data: map[string]any{"escalation_id": taken, "member_id": uuid.New(), "source": "chat"}}, true},
		{now.Add(-35 * time.Minute), outbox.Event{Type: "safety.EscalationTaken", Version: 1, Data: map[string]any{"escalation_id": taken, "taken_by": uuid.New()}}, true},
		{now.Add(-20 * time.Minute), outbox.Event{Type: "safety.EscalationOpened", Version: 1, Data: map[string]any{"escalation_id": open, "member_id": uuid.New(), "source": "checkin"}}, true},
		{now.Add(-10 * time.Minute), outbox.Event{Type: "safety.EscalationOpened", Version: 1, Data: map[string]any{"escalation_id": closed, "member_id": uuid.New(), "source": "chat"}}, true},
		{now.Add(-5 * time.Minute), outbox.Event{Type: "safety.EscalationClosed", Version: 1, Data: map[string]any{"escalation_id": closed}}, true},
		// Unpublished for 90 s: the outbox lag.
		{now.Add(-90 * time.Second), outbox.Event{Type: "notify.DeliveryRequested", Version: 1, Data: map[string]any{"request_id": uuid.New()}}, false},
		// An escalation older than the lookback is ignored.
		{now.Add(-60 * 24 * time.Hour), outbox.Event{Type: "safety.EscalationOpened", Version: 1, Data: map[string]any{"escalation_id": uuid.New()}}, true},
	}
	if err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		for _, ev := range events {
			id, err := outbox.Enqueue(ctx, q, ev.at, "", ev.e)
			if err != nil {
				return err
			}
			if ev.sent {
				if err := q.MarkEventsPublished(ctx, store.MarkEventsPublishedParams{PublishedAt: &now, Ids: []uuid.UUID{id}}); err != nil {
					return err
				}
			}
		}
		for _, due := range []time.Duration{-7 * time.Minute, -time.Minute, time.Hour} {
			if _, err := q.InsertTimer(ctx, store.InsertTimerParams{Kind: "test.Timer", SubjectID: uuid.New(), DueAt: now.Add(due), Payload: []byte("{}")}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	s := &telemetry.Sampler{Pool: pool, Clock: fixedClock{now}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := s.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		g    prometheus.Gauge
		want float64
	}{
		"outbox lag":              {telemetry.OutboxOldestAge, 90},
		"timers due":              {telemetry.TimersDue, 2},
		"oldest overdue timer":    {telemetry.TimersOldestOverdue, 7 * 60},
		"untaken escalations":     {telemetry.EscalationsUntaken, 1},
		"oldest untaken":          {telemetry.EscalationsOldestUntaken, 20 * 60},
		"last success (unix sec)": {telemetry.SamplerLastSuccess, float64(now.Unix())},
	} {
		if got := gaugeValue(t, c.g); got != c.want {
			t.Errorf("%s = %v, want %v", name, got, c.want)
		}
	}

	// Everything published, taken and done: the gauges return to 0.
	if _, err := pool.Exec(ctx, "UPDATE platform.outbox SET published_at = $1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE platform.timers SET done_at = $1", now); err != nil {
		t.Fatal(err)
	}
	if err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := outbox.Enqueue(ctx, store.New(tx), now.Add(-time.Minute), "", outbox.Event{Type: "safety.EscalationTaken", Version: 1, Data: map[string]any{"escalation_id": open, "taken_by": uuid.New()}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, telemetry.EscalationsUntaken); got != 0 {
		t.Errorf("untaken after take = %v, want 0", got)
	}
	for name, g := range map[string]prometheus.Gauge{
		"oldest untaken": telemetry.EscalationsOldestUntaken, "timers due": telemetry.TimersDue,
		"oldest overdue": telemetry.TimersOldestOverdue,
	} {
		if got := gaugeValue(t, g); got != 0 {
			t.Errorf("%s = %v, want 0", name, got)
		}
	}
	// The new Taken event is unpublished; lag is its age.
	if got := gaugeValue(t, telemetry.OutboxOldestAge); got != 60 {
		t.Errorf("outbox lag = %v, want 60", got)
	}
}
