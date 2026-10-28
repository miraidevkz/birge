package adapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/circle/adapter"
	"birge/internal/domains/circle/circletest"
	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

const fakeName = "Example"

type env struct {
	pool    *pgxpool.Pool
	svc     *service.Service
	members *circletest.FakeMembers
	clock   *circletest.Clock
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testinfra.Postgres(t)
	members := circletest.NewFakeMembers()
	clk := &circletest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	return &env{pool: pool, members: members, clock: clk, svc: &service.Service{
		Store: &adapter.PostgresStore{Pool: pool}, Members: members, Blocks: members,
		Clock: clk, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service.Settings{
			RequestTTL: 7 * 24 * time.Hour, MatchInterval: time.Hour,
			AskMetAfter: 3 * 24 * time.Hour, RematchCooldown: 30 * 24 * time.Hour,
		},
	}}
}

func (e *env) member(verified bool, district *uuid.UUID) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Put(p.MemberID, model.Profile{
		Verified: verified, DistrictID: district, Lang: "ru", FirstName: fakeName,
		Children: []model.Child{{BirthYear: 2018, Needs: []string{"autism"}}},
	})
	return p
}

func key() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTeaRequestedOutboxPostgres(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	a := e.member(true, &d)
	b := e.member(true, &d)

	idem := key()
	ra, err := e.svc.CreateTeaRequest(ctx, a, idem, model.FormatMeeting, model.Preferences{SameDistrict: true})
	if err != nil {
		t.Fatal(err)
	}
	// Replay with the same Idempotency-Key and a conflicting second open
	// request: neither may add an event.
	if again, err := e.svc.CreateTeaRequest(ctx, a, idem, model.FormatMeeting, model.Preferences{SameDistrict: true}); err != nil || again.ID != ra.ID {
		t.Fatalf("replay: %v %v", again.ID, err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatCall, model.Preferences{}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("second open: %v", err)
	}
	e.clock.T = e.clock.T.Add(time.Minute) // occurred_at orders the rows below
	rb, err := e.svc.CreateTeaRequest(ctx, b, key(), model.FormatCall, model.Preferences{SharedNeeds: true})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := e.pool.Query(ctx, `SELECT event_version, payload FROM platform.outbox WHERE event_type=$1 ORDER BY occurred_at, id`, model.EventTeaRequested)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []model.TeaRequestedData
	for rows.Next() {
		var version int
		var payload []byte
		if err := rows.Scan(&version, &payload); err != nil {
			t.Fatal(err)
		}
		if version != model.EventVersion {
			t.Fatalf("version = %d", version)
		}
		testinfra.ValidateEventPayload(t, "circle.TeaRequested.v1.json", payload)
		var data model.TeaRequestedData
		if err := json.Unmarshal(payload, &data); err != nil {
			t.Fatal(err)
		}
		got = append(got, data)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []model.TeaRequestedData{
		{RequestID: ra.ID, MemberID: a.MemberID, Format: model.FormatMeeting},
		{RequestID: rb.ID, MemberID: b.MemberID, Format: model.FormatCall},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("TeaRequested = %+v, want %+v", got, want)
	}
}

func TestOneOpenRequestAndTimersPostgres(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	a := e.member(true, &d)

	r, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatMeeting, model.Preferences{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatCall, model.Preferences{}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("second open: %v", err)
	}
	if count(t, e.pool, `SELECT count(*) FROM circle.tea_requests WHERE member_id=$1 AND status='open'`, a.MemberID) != 1 {
		t.Fatal("expected one open")
	}
	if count(t, e.pool, `SELECT count(*) FROM platform.timers WHERE kind=$1 AND subject_id=$2 AND done_at IS NULL`, model.TimerRequestExpire, r.ID) != 1 {
		t.Fatal("expected expire timer")
	}
	if count(t, e.pool, `SELECT count(*) FROM platform.timers WHERE kind=$1 AND done_at IS NULL`, model.TimerMatchRun) != 1 {
		t.Fatal("expected MatchRun timer")
	}

	// Fire MatchRun early: advance clock and due_at.
	_, err = e.pool.Exec(ctx, `UPDATE platform.timers SET due_at=$1 WHERE kind=$2 AND done_at IS NULL`, e.clock.T, model.TimerMatchRun)
	if err != nil {
		t.Fatal(err)
	}
	b := e.member(true, &d)
	if _, err := e.svc.CreateTeaRequest(ctx, b, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}

	handlers := &adapter.TimerHandlers{Service: e.svc}
	worker := &timers.Worker{
		Pool: e.pool, Clock: e.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: handlers.Handlers(),
	}
	fired, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("expected MatchRun to fire")
	}
	if count(t, e.pool, `SELECT count(*) FROM circle.matches`) != 1 {
		t.Fatal("expected one match")
	}
	if count(t, e.pool, `SELECT count(*) FROM platform.timers WHERE kind=$1 AND done_at IS NULL`, model.TimerMatchRun) != 1 {
		t.Fatal("MatchRun must re-schedule")
	}
	if count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type=$1`, model.EventDeliveryRequested) != 2 {
		t.Fatal("expected two match_proposed deliveries")
	}

	var payload []byte
	if err := e.pool.QueryRow(ctx, `SELECT payload FROM platform.outbox WHERE event_type=$1 LIMIT 1`, model.EventDeliveryRequested).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if jsonContains(payload, fakeName) || jsonContains(payload, "autism") {
		t.Fatalf("delivery payload leaked personal data: %s", payload)
	}
}

func TestSymmetricMetAndBlockedPostgres(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	a := e.member(true, &d)
	b := e.member(true, &d)
	c := e.member(true, &d)
	e.members.Block(a.MemberID, c.MemberID)

	if _, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, b, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, c, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}

	_, _ = e.pool.Exec(ctx, `UPDATE platform.timers SET due_at=$1 WHERE kind=$2 AND done_at IS NULL`, e.clock.T, model.TimerMatchRun)
	worker := &timers.Worker{
		Pool: e.pool, Clock: e.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: (&adapter.TimerHandlers{Service: e.svc}).Handlers(),
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	// a and c are blocked: only a-b or b-c can pair. With three and a-c blocked,
	// greedy may pair a-b (leaving c) or b-c (leaving a). Never a-c.
	n := count(t, e.pool, `SELECT count(*) FROM circle.matches`)
	if n != 1 {
		t.Fatalf("matches=%d", n)
	}
	var ma, mb uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT member_a, member_b FROM circle.matches`).Scan(&ma, &mb); err != nil {
		t.Fatal(err)
	}
	lo, hi := model.CanonicalPair(a.MemberID, c.MemberID)
	if ma == lo && mb == hi {
		t.Fatal("blocked pair was matched")
	}

	matches, err := e.svc.ListMyMatches(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		// a was left out (b-c paired); pair b and c through answers N/A — create fresh pair a-b
		// by cancelling leftover and re-matching. Simpler: accept whichever pair includes a, else use b.
		matches, err = e.svc.ListMyMatches(ctx, b)
		if err != nil || len(matches) == 0 {
			t.Fatalf("no matches: %v", err)
		}
		m := matches[0]
		other := m.MemberB
		if other == b.MemberID {
			other = m.MemberA
		}
		var pa, pb auth.Principal
		if other == a.MemberID {
			pa, pb = a, b
		} else {
			pa, pb = b, c
		}
		if _, err := e.svc.AnswerMatch(ctx, pa, m.ID, true); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.AnswerMatch(ctx, pb, m.ID, true); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.ConfirmMet(ctx, pa, m.ID, true); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.ConfirmMet(ctx, pb, m.ID, true); err != nil {
			t.Fatal(err)
		}
		ok, err := e.svc.Met(ctx, pa.MemberID, pb.MemberID)
		if err != nil || !ok {
			t.Fatalf("Met=%v %v", ok, err)
		}
		ok, err = e.svc.Met(ctx, pb.MemberID, pa.MemberID)
		if err != nil || !ok {
			t.Fatalf("Met symmetric=%v %v", ok, err)
		}
		return
	}
	m := matches[0]
	if _, err := e.svc.AnswerMatch(ctx, a, m.ID, true); err != nil {
		t.Fatal(err)
	}
	other := m.MemberB
	if other == a.MemberID {
		other = m.MemberA
	}
	var otherP auth.Principal
	switch other {
	case b.MemberID:
		otherP = b
	case c.MemberID:
		otherP = c
	}
	if _, err := e.svc.AnswerMatch(ctx, otherP, m.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ConfirmMet(ctx, a, m.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ConfirmMet(ctx, otherP, m.ID, true); err != nil {
		t.Fatal(err)
	}
	ok, err := e.svc.Met(ctx, a.MemberID, other)
	if err != nil || !ok {
		t.Fatalf("Met=%v %v", ok, err)
	}
	ok, err = e.svc.Met(ctx, other, a.MemberID)
	if err != nil || !ok {
		t.Fatalf("Met symmetric=%v %v", ok, err)
	}
	if count(t, e.pool, `SELECT count(*) FROM circle.acquaintances`) != 1 {
		t.Fatal("expected one acquaintance")
	}
	if count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type=$1`, model.EventAcquaintanceEstablished) != 1 {
		t.Fatal("expected AcquaintanceEstablished")
	}
}

func TestRequestExpireTimerPostgres(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	a := e.member(true, &d)
	r, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatMeeting, model.Preferences{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.pool.Exec(ctx, `UPDATE platform.timers SET due_at=$1 WHERE kind=$2 AND subject_id=$3`, e.clock.T, model.TimerRequestExpire, r.ID)
	worker := &timers.Worker{
		Pool: e.pool, Clock: e.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: (&adapter.TimerHandlers{Service: e.svc}).Handlers(),
	}
	// May fire MatchRun first if also due — set MatchRun far future.
	_, _ = e.pool.Exec(ctx, `UPDATE platform.timers SET due_at=$1 WHERE kind=$2`, e.clock.T.Add(24*time.Hour), model.TimerMatchRun)
	fired, err := worker.RunOnce(ctx)
	if err != nil || !fired {
		t.Fatalf("fired=%v err=%v", fired, err)
	}
	if count(t, e.pool, `SELECT count(*) FROM circle.tea_requests WHERE id=$1 AND status='expired'`, r.ID) != 1 {
		t.Fatal("expected expired")
	}
}

func jsonContains(raw []byte, s string) bool {
	return strings.Contains(string(raw), s)
}
