package adapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/analytics/adapter"
	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/outbox"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

// clock is a settable test clock shared by the service and the timer worker.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.t = t }

type cohorts struct{ agg model.CohortAggregates }

func (c cohorts) CohortAggregates(context.Context, time.Time, time.Time) (model.CohortAggregates, error) {
	return c.agg, nil
}

type env struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	clock    *clock
	svc      *service.Service
	consumer *adapter.FactsConsumer
	key      []byte
	logs     *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testinfra.Postgres(t)
	clk := &clock{t: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)}
	logs := &bytes.Buffer{}
	key := bytes.Repeat([]byte{0x42}, 32)
	svc := &service.Service{
		Store: &adapter.PostgresStore{Pool: pool},
		Cohorts: cohorts{agg: model.CohortAggregates{
			StressBaseline: model.ScoreStats{Count: 2, Mean: 10, Median: 10},
		}},
		Clock:    clk,
		Logger:   slog.New(slog.NewJSONHandler(logs, nil)),
		Settings: service.Settings{K: 1, TimeZone: "Asia/Almaty", Key: key},
	}
	return &env{
		t: t, ctx: context.Background(), pool: pool, clock: clk, svc: svc, key: key, logs: logs,
		consumer: &adapter.FactsConsumer{Service: svc, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
	}
}

// send delivers one event through the consumer, as JetStream would, after
// checking its payload against the event contract.
func (e *env) send(id uuid.UUID, typ string, at time.Time, data map[string]any) {
	e.t.Helper()
	payload, err := json.Marshal(data)
	if err != nil {
		e.t.Fatal(err)
	}
	testinfra.ValidateEventPayload(e.t, typ+".v1.json", payload)
	body, err := json.Marshal(outbox.Envelope{ID: id, Type: typ, Version: 1, OccurredAt: at, Data: payload})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.consumer.HandleMessage(e.ctx, body); err != nil {
		e.t.Fatalf("%s: %v", typ, err)
	}
}

func (e *env) emit(typ string, at time.Time, data map[string]any) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	e.send(id, typ, at, data)
	return id
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

var admin = auth.Principal{IdentityID: uuid.MustParse("019a3000-0000-7000-8000-00000000ad01"), Staff: true, Roles: []string{"admin"}, MFA: true}

func utc(month time.Month, day, hour, minute int) time.Time {
	return time.Date(2026, month, day, hour, minute, 0, 0, time.UTC)
}

// seed is the event sequence of March 2026 behind TestMetricsFromEvents.
type seed struct {
	m1, m2, m3 uuid.UUID
	sos1, esc1 uuid.UUID
	teaEvent   uuid.UUID
}

func (e *env) seed() seed {
	s := seed{m1: uuid.New(), m2: uuid.New(), m3: uuid.New(), sos1: uuid.New(), esc1: uuid.New()}
	day := func(m uuid.UUID, d string) {
		e.emit(model.EventMemberActiveDay, utc(3, 1, 0, 0), map[string]any{"member_id": m.String(), "day": d})
	}
	// Activity. m1: first day 2 March, back on day 28 and day 56. m2: one
	// day. m3: first seen in February, so not in the March cohort.
	day(s.m1, "2026-03-02")
	day(s.m1, "2026-03-03")
	day(s.m1, "2026-03-30")
	day(s.m1, "2026-04-27")
	day(s.m2, "2026-03-10")
	day(s.m3, "2026-02-20")
	day(s.m3, "2026-03-05")

	// Tea: two requests, one pair, one meeting. The first request is
	// delivered twice: processed_events keeps one fact.
	req1, req2, match := uuid.New(), uuid.New(), uuid.New()
	s.teaEvent = uuid.New()
	for range 2 {
		e.send(s.teaEvent, model.EventTeaRequested, utc(3, 5, 9, 0), map[string]any{"request_id": req1.String(), "member_id": s.m1.String(), "format": "call"})
	}
	e.emit(model.EventTeaRequested, utc(3, 6, 9, 0), map[string]any{"request_id": req2.String(), "member_id": s.m2.String(), "format": "call"})
	e.emit(model.EventMatchAccepted, utc(3, 7, 9, 0), map[string]any{"match_id": match.String(), "member_a": s.m1.String(), "member_b": s.m2.String()})
	e.emit(model.EventAcquaintanceEstablished, utc(3, 20, 9, 0), map[string]any{"member_a": s.m1.String(), "member_b": s.m2.String()})

	// SOS: sos1 (m1) answered after 12 minutes (a second accept later does
	// not count) and closed; sos2 (m2) answered after 20 minutes, open;
	// sos3 (m3) raised in April, outside the period.
	sos2, sos3, responder := uuid.New(), uuid.New(), uuid.New()
	e.emit(model.EventSosRaised, utc(3, 10, 10, 0), map[string]any{"sos_id": s.sos1.String(), "member_id": s.m1.String(), "type": "childcare"})
	e.emit(model.EventSosResponseAccepted, utc(3, 10, 10, 12), map[string]any{"sos_id": s.sos1.String(), "author_id": s.m1.String(), "responder_id": responder.String()})
	e.emit(model.EventSosResponseAccepted, utc(3, 10, 10, 30), map[string]any{"sos_id": s.sos1.String(), "author_id": s.m1.String(), "responder_id": uuid.NewString()})
	e.emit(model.EventSosClosed, utc(3, 10, 11, 0), map[string]any{"sos_id": s.sos1.String()})
	e.emit(model.EventSosRaised, utc(3, 11, 8, 0), map[string]any{"sos_id": sos2.String(), "member_id": s.m2.String(), "type": "other"})
	e.emit(model.EventSosResponseAccepted, utc(3, 11, 8, 20), map[string]any{"sos_id": sos2.String(), "author_id": s.m2.String(), "responder_id": responder.String()})
	e.emit(model.EventSosRaised, utc(4, 2, 8, 0), map[string]any{"sos_id": sos3.String(), "member_id": s.m3.String(), "type": "other"})

	// Escalations: esc1 (m1) taken after 10 minutes, esc2 (m2) after 40,
	// esc3 (m3) never.
	esc2, staff := uuid.New(), uuid.New()
	e.emit(model.EventEscalationOpened, utc(3, 12, 9, 0), map[string]any{"escalation_id": s.esc1.String(), "member_id": s.m1.String(), "source": "checkin"})
	e.emit(model.EventEscalationTaken, utc(3, 12, 9, 10), map[string]any{"escalation_id": s.esc1.String(), "taken_by": staff.String()})
	e.emit(model.EventEscalationOpened, utc(3, 13, 9, 0), map[string]any{"escalation_id": esc2.String(), "member_id": s.m2.String(), "source": "menu"})
	e.emit(model.EventEscalationTaken, utc(3, 13, 9, 40), map[string]any{"escalation_id": esc2.String(), "taken_by": staff.String()})
	e.emit(model.EventEscalationOpened, utc(3, 14, 9, 0), map[string]any{"escalation_id": uuid.NewString(), "member_id": s.m3.String(), "source": "assistant"})

	// Rights: stored without an actor, reported suppressed.
	msg := uuid.New()
	e.emit(model.EventQuestionAnswered, utc(3, 15, 9, 0), map[string]any{"message_id": msg.String(), "scope": "rights", "source": "knowledge"})
	e.emit(model.EventReplyRated, utc(3, 15, 9, 5), map[string]any{"message_id": msg.String(), "helped": true})
	return s
}

func cellIs(t *testing.T, name string, c model.Cell, members int, value float64) {
	t.Helper()
	if c.Suppressed || c.Members != members || c.Value != value {
		t.Errorf("%s = %+v, want members %d value %v", name, c, members, value)
	}
}

func TestMetricsFromEvents(t *testing.T) {
	e := newEnv(t)
	s := e.seed()
	from, to := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)

	m, err := e.svc.Metrics(e.ctx, admin, from, to)
	if err != nil {
		t.Fatal(err)
	}
	// Weeks overlapping March: 23 Feb (1 March only), 2, 9, 16, 23, 30 March.
	wantWeeks := []int{0, 2, 1, 0, 0, 1}
	if len(m.ActiveWeekly) != len(wantWeeks) {
		t.Fatalf("weekly buckets = %d", len(m.ActiveWeekly))
	}
	for i, w := range wantWeeks {
		b := m.ActiveWeekly[i]
		if w == 0 {
			if !b.Cell.Suppressed {
				t.Errorf("week %s = %+v, want suppressed (no members)", b.Start.Format(time.DateOnly), b.Cell)
			}
			continue
		}
		cellIs(t, "week "+b.Start.Format(time.DateOnly), b.Cell, w, float64(w))
	}
	if len(m.ActiveMonthly) != 1 || !m.ActiveMonthly[0].Start.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("monthly = %+v", m.ActiveMonthly)
	}
	cellIs(t, "March", m.ActiveMonthly[0].Cell, 3, 3)
	cellIs(t, "retention 4w", m.Retention4w, 2, 0.5)
	cellIs(t, "retention 8w", m.Retention8w, 2, 0.5)
	cellIs(t, "tea requests", m.TeaRequests, 2, 2)
	cellIs(t, "tea pairs", m.TeaPairs, 2, 1)
	cellIs(t, "tea meetings", m.TeaMeetings, 2, 1)
	cellIs(t, "sos raised", m.SosRaised, 2, 2)
	cellIs(t, "sos closed share", m.SosClosedShare, 2, 0.5)
	cellIs(t, "sos first response median", m.SosFirstResponseMedianMinutes, 2, 16)
	cellIs(t, "escalations opened", m.EscalationsOpened, 3, 3)
	cellIs(t, "escalations taken within 15m", m.EscalationsTakenWithin15mShare, 3, 1.0/3)
	cellIs(t, "stress baseline mean", m.StressBaseline.Mean, 2, 10)
	for name, c := range map[string]model.Cell{
		"stress week8": m.StressWeek8.Mean, "isolation": m.IsolationBefore.Median,
		"rights answered": m.RightsAnsweredFromKnowledge, "rights helped": m.RightsHelpedShare,
	} {
		if !c.Suppressed {
			t.Errorf("%s = %+v, want suppressed", name, c)
		}
	}

	// k = 3: every cell from fewer than 3 members is suppressed, 3 stays.
	e.svc.Settings.K = 3
	m3, err := e.svc.Metrics(e.ctx, admin, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if !m3.TeaRequests.Suppressed || !m3.SosRaised.Suppressed || !m3.ActiveWeekly[1].Cell.Suppressed {
		t.Error("cells from 2 members shown with k=3")
	}
	cellIs(t, "escalations opened with k=3", m3.EscalationsOpened, 3, 3)
	cellIs(t, "March with k=3", m3.ActiveMonthly[0].Cell, 3, 3)

	// Every read and the export are audited, by the admin, as admin.
	csv, name, err := e.svc.MetricsCSV(e.ctx, admin, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if name != "birge-metrics-2026-03-01-2026-03-31.csv" || !strings.Contains(string(csv), "escalations.opened,,3,3,false\n") {
		t.Fatalf("csv %s:\n%s", name, csv)
	}
	if n := e.count(`SELECT count(*) FROM platform.audit_log WHERE action = $1 AND actor_id = $2 AND actor_role = 'admin' AND object_type = $3`,
		service.AuditActionRead, admin.IdentityID, service.AuditObject); n != 2 {
		t.Errorf("read audit entries = %d, want 2", n)
	}
	if n := e.count(`SELECT count(*) FROM platform.audit_log WHERE action = $1`, service.AuditActionExport); n != 1 {
		t.Errorf("export audit entries = %d, want 1", n)
	}

	// Nothing but facts was stored: no IDs in clear, no payload.
	for _, id := range []uuid.UUID{s.m1, s.m2, s.sos1, s.esc1} {
		if n := e.count(`SELECT count(*) FROM analytics.facts f WHERE f::text LIKE '%' || $1 || '%'`, id.String()); n != 0 {
			t.Errorf("a fact contains the ID %s in clear", id)
		}
	}
	if n := e.count(`SELECT count(*) FROM analytics.facts WHERE event_type = $1`, model.EventTeaRequested); n != 2 {
		t.Errorf("tea request facts = %d, want 2 (one redelivery dropped)", n)
	}
	if strings.Contains(e.logs.String(), s.m1.String()) {
		t.Error("logs name a member")
	}
}

func TestDeletionAndLateEvents(t *testing.T) {
	e := newEnv(t)
	s := e.seed()
	del := &service.Deletion{Store: &adapter.PostgresStore{Pool: e.pool}, Key: e.key, Clock: e.clock}
	m1 := model.Pseudonym(e.key, s.m1)
	before := e.count(`SELECT count(*) FROM analytics.facts`)
	own := e.count(`SELECT count(*) FROM analytics.facts WHERE actor = $1`, m1)
	if own == 0 {
		t.Fatal("seed has no facts of m1")
	}

	for range 2 { // idempotent
		if err := del.DeleteMember(e.ctx, s.m1); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.count(`SELECT count(*) FROM analytics.facts WHERE actor = $1`, m1); n != 0 {
		t.Fatalf("facts of the deleted member = %d", n)
	}
	// Her SOS closing and her escalation's taking carry no actor and go too.
	for _, subj := range [][]byte{model.SubjectPseudonym(e.key, s.sos1), model.SubjectPseudonym(e.key, s.esc1)} {
		if n := e.count(`SELECT count(*) FROM analytics.facts WHERE subject = $1`, subj); n != 0 {
			t.Errorf("facts about her SOS or escalation = %d", n)
		}
	}
	// 2 = the SOS closing and the escalation taking.
	if after := e.count(`SELECT count(*) FROM analytics.facts`); after != before-own-2 {
		t.Errorf("facts after deletion = %d, want %d", after, before-own-2)
	}
	if n := e.count(`SELECT count(*) FROM analytics.facts WHERE actor = $1`, model.Pseudonym(e.key, s.m2)); n == 0 {
		t.Error("another member's facts were deleted")
	}
	if n := e.count(`SELECT count(*) FROM platform.audit_log WHERE action = 'analytics.member_deleted' AND object_id = $1`, s.m1); n != 1 {
		t.Errorf("deletion audit entries = %d, want 1", n)
	}

	// A late event of hers (delivered after the deletion) is not counted.
	e.emit(model.EventMemberActiveDay, utc(6, 29, 0, 0), map[string]any{"member_id": s.m1.String(), "day": "2026-06-29"})
	if n := e.count(`SELECT count(*) FROM analytics.facts WHERE actor = $1`, m1); n != 0 {
		t.Fatal("a late event of a deleted member was stored")
	}
}

func TestRetentionRollsUpAndDeletes(t *testing.T) {
	e := newEnv(t)
	s := e.seed()
	if err := (&service.Deletion{Store: &adapter.PostgresStore{Pool: e.pool}, Key: e.key, Clock: e.clock}).DeleteMember(e.ctx, s.m3); err != nil {
		t.Fatal(err)
	}
	// A fact inside the retention period stays.
	e.clock.Set(time.Date(2028, 3, 20, 12, 0, 0, 0, time.UTC))
	e.emit(model.EventMemberActiveDay, utc(3, 1, 0, 0), map[string]any{"member_id": s.m2.String(), "day": "2026-07-01"})
	if err := e.svc.EnsureRetentionTimer(e.ctx); err != nil {
		t.Fatal(err)
	}
	stored := e.count(`SELECT count(*) FROM analytics.facts`)
	cutoff := model.RetentionCutoff(e.clock.Now(), mustLoc(t))
	old := e.count(`SELECT count(*) FROM analytics.facts WHERE occurred_at < $1`, cutoff)
	if old == 0 || old == stored {
		t.Fatalf("seed: %d of %d facts older than the cutoff", old, stored)
	}

	e.clock.Set(time.Date(2028, 3, 21, 4, 0, 0, 0, time.UTC)) // after the 03:30 UTC run
	worker := &timers.Worker{
		Pool: e.pool, Clock: e.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: (&adapter.TimerHandlers{Service: e.svc}).Handlers(),
	}
	fired, err := worker.RunOnce(e.ctx)
	if err != nil || !fired {
		t.Fatalf("retention timer fired=%v err=%v", fired, err)
	}
	if n := e.count(`SELECT count(*) FROM analytics.facts`); n != stored-old {
		t.Errorf("facts after the sweep = %d, want %d", n, stored-old)
	}
	if n := e.count(`SELECT coalesce(sum(facts), 0)::int FROM analytics.weekly_rollups`); n != old {
		t.Errorf("rolled-up facts = %d, want %d", n, old)
	}
	if n := e.count(`SELECT count(*) FROM analytics.weekly_rollups WHERE extract(isodow FROM week) <> 1`); n != 0 {
		t.Errorf("%d roll-up weeks do not start on Monday", n)
	}
	if n := e.count(`SELECT count(*) FROM analytics.weekly_rollups WHERE week = '2026-03-02' AND event_type = $1 AND code = ''`, model.EventMemberActiveDay); n != 1 {
		t.Errorf("week of 2 March active-day roll-up rows = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM analytics.erased_actors`); n != 0 {
		t.Errorf("erased pseudonyms older than a year = %d, want 0", n)
	}
	if n := e.count(`SELECT count(*) FROM platform.timers WHERE kind = $1 AND done_at IS NULL`, model.TimerRetention); n != 1 {
		t.Errorf("next retention runs scheduled = %d, want 1", n)
	}

	// A second sweep finds nothing new and changes no roll-up.
	var sum int
	if err := e.pool.QueryRow(e.ctx, `SELECT sum(facts) FROM analytics.weekly_rollups`).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	e.clock.Set(time.Date(2028, 3, 22, 4, 0, 0, 0, time.UTC))
	if fired, err := worker.RunOnce(e.ctx); err != nil || !fired {
		t.Fatalf("second sweep fired=%v err=%v", fired, err)
	}
	if n := e.count(`SELECT sum(facts)::int FROM analytics.weekly_rollups`); n != sum {
		t.Errorf("roll-ups changed on the second sweep: %d, want %d", n, sum)
	}
}

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestFactsConsumerJetStream(t *testing.T) {
	e := newEnv(t)
	nc, err := nats.Connect(testinfra.NATS(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateOrUpdateStream(e.ctx, jetstream.StreamConfig{Name: "EVENTS", Subjects: []string{"birge.events.>"}, Duplicates: time.Minute}); err != nil {
		t.Fatal(err)
	}
	e.consumer.JS, e.consumer.Stream = js, "EVENTS"
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- e.consumer.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	publish := func(typ string, data string) {
		t.Helper()
		env, _ := json.Marshal(outbox.Envelope{ID: uuid.New(), Type: typ, Version: 1, OccurredAt: e.clock.Now(), Data: json.RawMessage(data)})
		if _, err := js.Publish(e.ctx, outbox.Subject(typ), env, jetstream.WithMsgID(uuid.NewString())); err != nil {
			t.Fatal(err)
		}
	}
	// A malformed payload is terminated and does not block the next event.
	publish(model.EventSosRaised, `{"sos_id":"not-a-uuid"}`)
	publish(model.EventSosClosed, `{"sos_id":"`+uuid.NewString()+`"}`)
	for range 200 {
		if e.count(`SELECT count(*) FROM analytics.facts WHERE event_type = $1`, model.EventSosClosed) == 1 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("SosClosed fact not stored through JetStream")
}
