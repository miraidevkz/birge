package adapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"birge/internal/domains/chat/adapter"
	"birge/internal/domains/chat/chattest"
	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/outbox"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

// Clearly fake data.
const (
	fakeText = "fake hello from the example playground"
	fakeName = "Fakename"
)

type env struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	svc     *service.Service
	members *chattest.FakeMembers
	crisis  *chattest.FakeCrisis
	sealer  *chattest.FakeSealer
	bus     *chattest.Bus
	clock   *chattest.Clock
	logs    *bytes.Buffer
	worker  *timers.Worker
}

func newEnv(t *testing.T, settings service.Settings) *env {
	t.Helper()
	pool := testinfra.Postgres(t)
	e := &env{
		t: t, ctx: context.Background(), pool: pool,
		members: chattest.NewFakeMembers(), crisis: &chattest.FakeCrisis{}, sealer: &chattest.FakeSealer{},
		bus: &chattest.Bus{}, clock: &chattest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		logs: &bytes.Buffer{},
	}
	if settings.NewcomerHeld == 0 {
		settings.NewcomerHeld = -1
	}
	e.svc = &service.Service{
		Store: &adapter.PostgresStore{Pool: pool}, Sealer: e.sealer, Members: e.members, Blocks: e.members, Mentors: e.members,
		Crisis: e.crisis, Fanout: e.bus, Clock: e.clock, Logger: slog.New(slog.NewJSONHandler(&lockedWriter{w: e.logs}, nil)), Settings: settings,
	}
	e.worker = &timers.Worker{Pool: pool, Clock: e.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: (&adapter.TimerHandlers{Service: e.svc}).Handlers()}
	t.Cleanup(e.checkNoText)
	return e
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// checkNoText asserts that no message text reached logs, the outbox or a
// table column in plaintext.
func (e *env) checkNoText() {
	if strings.Contains(e.logs.String(), "fake hello") || strings.Contains(e.logs.String(), chattest.CrisisWord) {
		e.t.Error("message text in logs")
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM platform.outbox WHERE payload::text LIKE '%fake hello%' OR payload::text LIKE '%`+chattest.CrisisWord+`%'`).Scan(&n); err != nil {
		e.t.Error(err)
	}
	if n != 0 {
		e.t.Error("message text in outbox")
	}
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat.messages WHERE position('fake hello'::bytea in body) > 0`).Scan(&n); err != nil {
		e.t.Error(err)
	}
	if n != 0 {
		e.t.Error("plaintext in chat.messages")
	}
}

func (e *env) member(verified bool, district *uuid.UUID) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Add(p.MemberID, model.Profile{Verified: verified, FirstName: fakeName, Lang: "ru", DistrictID: district})
	return p
}

func staff(roles ...string) auth.Principal {
	return auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: roles}
}

func (e *env) topicRoom(code string) model.Room {
	e.t.Helper()
	r, err := e.svc.EnsureTopicRoom(e.ctx, code, "chat.room.topic."+code)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) join(p auth.Principal, roomID uuid.UUID) {
	e.t.Helper()
	if _, err := e.svc.JoinRoom(e.ctx, p, roomID); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) send(p auth.Principal, roomID uuid.UUID, text string) model.SendResult {
	e.t.Helper()
	res, err := e.svc.Send(e.ctx, p, roomID, uuid.Must(uuid.NewV7()), text)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) list(p auth.Principal, roomID uuid.UUID) []model.MessageView {
	e.t.Helper()
	zero := int64(0)
	v, err := e.svc.ListMessages(e.ctx, p, roomID, &zero, nil, 100)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *env) runTimers() {
	e.t.Helper()
	for range 1000 {
		fired, err := e.worker.RunOnce(e.ctx)
		if err != nil {
			e.t.Fatal(err)
		}
		if !fired {
			return
		}
	}
	e.t.Fatal("timers did not settle")
}

func (e *env) outbox(eventType string) []map[string]any {
	e.t.Helper()
	rows, err := e.pool.Query(e.ctx, `SELECT payload FROM platform.outbox WHERE event_type = $1 ORDER BY occurred_at, id`, eventType)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			e.t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestIdempotentSendPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	a := e.member(true, nil)
	e.join(a, room.ID)
	client := uuid.Must(uuid.NewV7())

	first, err := e.svc.Send(e.ctx, a, room.ID, client, fakeText)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]model.SendResult, 10)
	errs := make([]error, 10)
	for i := range 10 {
		wg.Go(func() { results[i], errs[i] = e.svc.Send(e.ctx, a, room.ID, client, fakeText) })
	}
	wg.Wait()
	for i := range 10 {
		if errs[i] != nil || !results[i].Replayed || results[i].Message.ID != first.Message.ID || results[i].Message.Seq != 1 {
			t.Fatalf("replay %d: %+v %v", i, results[i], errs[i])
		}
	}
	var n, lastSeq int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*), (SELECT last_seq FROM chat.rooms WHERE id = $1) FROM chat.messages`, room.ID).Scan(&n, &lastSeq); err != nil {
		t.Fatal(err)
	}
	if n != 1 || lastSeq != 1 {
		t.Fatalf("messages=%d last_seq=%d", n, lastSeq)
	}
	if v := first.Message; v.Text == nil || *v.Text != fakeText || v.Status != model.StatusVisible || !v.Mine {
		t.Fatalf("view %+v", v)
	}
	// Same client id concurrently in two rooms: one message, no sequence gap.
	room2 := e.topicRoom("hearing")
	e.join(a, room2.ID)
	client2 := uuid.Must(uuid.NewV7())
	for _, r := range []uuid.UUID{room.ID, room2.ID} {
		wg.Go(func() { _, _ = e.svc.Send(e.ctx, a, r, client2, fakeText) })
	}
	wg.Wait()
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM chat.messages WHERE client_id = $1`, client2).Scan(&n); err != nil || n != 1 {
		t.Fatalf("duplicate client id stored %d times (%v)", n, err)
	}
	var sum int
	if err := e.pool.QueryRow(e.ctx, `SELECT sum(last_seq) FROM chat.rooms`).Scan(&sum); err != nil || sum != 2 {
		t.Fatalf("sequence numbers leaked: sum(last_seq)=%d (%v)", sum, err)
	}
	// Invalid input.
	if _, err := e.svc.Send(e.ctx, a, room.ID, uuid.New(), fakeText); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("v4 client id: %v", err)
	}
	outsider := e.member(true, nil)
	if _, err := e.svc.Send(e.ctx, outsider, room.ID, uuid.Must(uuid.NewV7()), fakeText); !errors.Is(err, model.ErrNotMember) {
		t.Fatalf("outsider: %v", err)
	}
	if _, err := e.svc.Send(e.ctx, staff("moderator"), room.ID, uuid.Must(uuid.NewV7()), fakeText); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("staff send: %v", err)
	}
}

func TestSeqUnderConcurrencyPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	const n = 50
	senders := make([]auth.Principal, n)
	for i := range n {
		senders[i] = e.member(true, nil)
		e.join(senders[i], room.ID)
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			_, err := e.svc.Send(e.ctx, senders[i], room.ID, uuid.Must(uuid.NewV7()), fakeText)
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := e.pool.Query(e.ctx, `SELECT seq FROM chat.messages WHERE room_id = $1 ORDER BY seq`, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := int64(1)
	for rows.Next() {
		var s int64
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		if s != want {
			t.Fatalf("seq %d, want %d (gap or duplicate)", s, want)
		}
		want++
	}
	if want != n+1 {
		t.Fatalf("got %d messages, want %d", want-1, n)
	}
	if got := e.list(senders[0], room.ID); len(got) != n {
		t.Fatalf("listed %d", len(got))
	}
}

func TestCrisisMessageHeldEscalatedPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{RateLimit: 1})
	room := e.topicRoom("autism")
	author, reader := e.member(true, nil), e.member(true, nil)
	e.join(author, room.ID)
	e.join(reader, room.ID)
	e.send(author, room.ID, fakeText) // uses the only rate-limit token

	res := e.send(author, room.ID, "i "+chattest.CrisisWord+" today")
	if res.Message.Status != model.StatusHeld || res.Crisis == nil || len(res.Crisis.Phones) == 0 || res.Crisis.EscalationID == nil {
		t.Fatalf("crisis result %+v", res)
	}
	esc, ok := e.crisis.Escalation(author.MemberID)
	if !ok || esc != *res.Crisis.EscalationID || e.crisis.Sources[0] != model.EscalationSource {
		t.Fatalf("escalation %v %v %v", esc, ok, e.crisis.Sources)
	}
	for _, v := range e.list(reader, room.ID) {
		if v.ID == res.Message.ID {
			t.Fatal("crisis message visible to another member")
		}
	}
	mine := e.list(author, room.ID)
	if len(mine) != 2 || mine[1].Status != model.StatusHeld {
		t.Fatalf("author view %+v", mine)
	}
	if len(e.outbox(model.EventMessageHeld)) != 0 {
		t.Fatal("crisis message published to moderation")
	}
	var stored *uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `SELECT escalation_id FROM chat.messages WHERE id = $1`, res.Message.ID).Scan(&stored); err != nil || stored == nil || *stored != esc {
		t.Fatalf("escalation not recorded: %v %v", stored, err)
	}
	e.clock.Advance(time.Minute)
	e.runTimers() // the cancelled retry timer must not escalate again
	if e.crisis.Calls != 1 {
		t.Fatalf("OpenEscalation calls = %d", e.crisis.Calls)
	}

	// Safety down: the message is still stored held, the author still gets
	// the phones, and the timer opens the escalation later.
	other := e.member(true, nil)
	e.join(other, room.ID)
	e.crisis.SetErr(errors.New("safety down"))
	res2 := e.send(other, room.ID, chattest.CrisisWord)
	if res2.Crisis == nil || res2.Crisis.EscalationID != nil || res2.Message.Status != model.StatusHeld {
		t.Fatalf("deferred crisis %+v", res2)
	}
	e.clock.Advance(time.Minute)
	if _, err := e.worker.RunOnce(e.ctx); err == nil {
		t.Fatal("timer should fail while safety is down")
	}
	e.crisis.SetErr(nil)
	e.clock.Advance(time.Hour)
	e.runTimers()
	if _, ok := e.crisis.Escalation(other.MemberID); !ok {
		t.Fatal("escalation not opened by the retry timer")
	}
	if !strings.Contains(e.logs.String(), "ChatCrisisEscalationDeferred") {
		t.Fatal("deferred escalation not logged loudly")
	}

	// Replay returns the crisis notice again, without a second escalation.
	calls := e.crisis.Calls
	again, err := e.svc.Send(e.ctx, other, room.ID, mustClientID(t, e, res2.Message.ID), chattest.CrisisWord)
	if err != nil || !again.Replayed || again.Crisis == nil || e.crisis.Calls != calls {
		t.Fatalf("replay %+v %v calls=%d", again, err, e.crisis.Calls)
	}

	// Not a member: nothing stored, but the crisis notice and escalation still happen.
	outsider := e.member(true, nil)
	res3, err := e.svc.Send(e.ctx, outsider, room.ID, uuid.Must(uuid.NewV7()), chattest.CrisisWord)
	if !errors.Is(err, model.ErrNotMember) || res3.Crisis == nil || res3.Crisis.EscalationID == nil {
		t.Fatalf("outsider crisis %+v %v", res3, err)
	}
}

func mustClientID(t *testing.T, e *env, messageID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `SELECT client_id FROM chat.messages WHERE id = $1`, messageID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLinkAndPhoneHoldPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	a, b := e.member(true, nil), e.member(true, nil)
	e.join(a, room.ID)
	e.join(b, room.ID)
	link := e.send(a, room.ID, "fake offer at https://example.com")
	phone := e.send(a, room.ID, "fake call +7 700 000 00 00")
	ok := e.send(a, room.ID, fakeText)
	if link.Message.Status != model.StatusHeld || phone.Message.Status != model.StatusHeld || ok.Message.Status != model.StatusVisible {
		t.Fatalf("statuses %s %s %s", link.Message.Status, phone.Message.Status, ok.Message.Status)
	}
	held := e.outbox(model.EventMessageHeld)
	if len(held) != 2 || held[0]["reason"] != model.HoldLink || held[1]["reason"] != model.HoldPhone || held[0]["message_id"] != link.Message.ID.String() {
		t.Fatalf("held events %v", held)
	}
	if got := e.list(b, room.ID); len(got) != 1 || got[0].ID != ok.Message.ID {
		t.Fatalf("reader sees %+v", got)
	}
	// Restoring a held message makes it visible and schedules its push.
	if err := e.svc.ModerateRoom(e.ctx, staff("moderator"), room.ID, model.Moderation{Action: model.ActionRestoreMessage, MessageID: &link.Message.ID}); err != nil {
		t.Fatal(err)
	}
	if got := e.list(b, room.ID); len(got) != 2 {
		t.Fatalf("restored not visible: %+v", got)
	}
}

func TestNewcomerHoldPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{NewcomerHeld: 2})
	room := e.topicRoom("autism")
	a := e.member(true, nil)
	e.join(a, room.ID)
	got := []string{
		e.send(a, room.ID, fakeText).Message.Status,
		e.send(a, room.ID, fakeText).Message.Status,
		e.send(a, room.ID, fakeText).Message.Status,
	}
	if got[0] != model.StatusHeld || got[1] != model.StatusHeld || got[2] != model.StatusVisible {
		t.Fatalf("statuses %v", got)
	}
	held := e.outbox(model.EventMessageHeld)
	if len(held) != 2 || held[0]["reason"] != model.HoldNewcomer {
		t.Fatalf("held events %v", held)
	}
	// Direct rooms are not subject to the newcomer hold.
	b := e.member(true, nil)
	dr, err := e.svc.OpenDirectRoom(e.ctx, uuid.New(), model.OriginMatch, uuid.New(), a.MemberID, b.MemberID)
	if err != nil {
		t.Fatal(err)
	}
	if s := e.send(b, dr.ID, fakeText).Message.Status; s != model.StatusVisible {
		t.Fatalf("direct newcomer status %s", s)
	}
}

func TestLimitsPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{RateLimit: 2, RateWindow: time.Minute})
	room := e.topicRoom("autism")
	a := e.member(true, nil)
	e.join(a, room.ID)
	e.send(a, room.ID, fakeText)
	e.send(a, room.ID, fakeText)
	if _, err := e.svc.Send(e.ctx, a, room.ID, uuid.Must(uuid.NewV7()), fakeText); !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("rate limit: %v", err)
	}
	e.clock.Advance(2 * time.Minute)
	slow := 60
	if err := e.svc.ModerateRoom(e.ctx, staff("admin"), room.ID, model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: &slow}); err != nil {
		t.Fatal(err)
	}
	e.send(a, room.ID, fakeText)
	e.clock.Advance(10 * time.Second)
	if _, err := e.svc.Send(e.ctx, a, room.ID, uuid.Must(uuid.NewV7()), fakeText); !errors.Is(err, model.ErrSlowMode) {
		t.Fatalf("slow mode: %v", err)
	}
	// Crisis bypasses the limits.
	if res := e.send(a, room.ID, chattest.CrisisWord); res.Crisis == nil {
		t.Fatal("crisis refused by slow mode")
	}
}

func TestBlockedFilteringBothDirectionsPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	a, b, c := e.member(true, nil), e.member(true, nil), e.member(true, nil)
	for _, p := range []auth.Principal{a, b, c} {
		e.join(p, room.ID)
	}
	e.members.Block(a.MemberID, b.MemberID) // a blocked b
	ma := e.send(a, room.ID, fakeText)
	mb := e.send(b, room.ID, fakeText)
	has := func(vs []model.MessageView, id uuid.UUID) bool {
		for _, v := range vs {
			if v.ID == id {
				return true
			}
		}
		return false
	}
	if la := e.list(a, room.ID); has(la, mb.Message.ID) || len(la) != 1 {
		t.Fatalf("blocker sees blocked: %+v", la)
	}
	if lb := e.list(b, room.ID); has(lb, ma.Message.ID) || len(lb) != 1 {
		t.Fatalf("blocked sees blocker: %+v", lb)
	}
	if lc := e.list(c, room.ID); len(lc) != 2 {
		t.Fatalf("third member sees %d", len(lc))
	}
	// Rendering for the gateway applies the same filter.
	o, err := e.svc.LoadOpened(e.ctx, ma.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := e.svc.Render(e.ctx, b.MemberID, o); ok || err != nil {
		t.Fatalf("render to blocked: %v %v", ok, err)
	}
	// Push: neither a nor b is pushed for the other's message; c is pushed.
	e.clock.Advance(time.Second)
	e.runTimers()
	pushes := e.outbox(model.EventDeliveryRequested)
	for _, d := range pushes {
		if to := d["recipient"].(map[string]any)["member_id"]; to == b.MemberID.String() || to == a.MemberID.String() {
			t.Fatal("push across a block")
		}
	}
	if len(pushes) != 1 {
		t.Fatalf("pushes %v", pushes)
	}
	// Reporting a message she cannot see is "not found".
	if err := e.svc.ReportMessage(e.ctx, b, ma.Message.ID, "spam"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("report blocked: %v", err)
	}
	// Membership down: fail closed.
	e.members.Err = errors.New("membership down")
	zero := int64(0)
	if _, err := e.svc.ListMessages(e.ctx, c, room.ID, &zero, nil, 50); err == nil {
		t.Fatal("list with membership down succeeded")
	}
	e.members.Err = nil
}

func TestPushThrottleAndMutePostgres(t *testing.T) {
	e := newEnv(t, service.Settings{PushInterval: 10 * time.Minute})
	room := e.topicRoom("autism")
	a, b, c := e.member(true, nil), e.member(true, nil), e.member(true, nil)
	for _, p := range []auth.Principal{a, b, c} {
		e.join(p, room.ID)
	}
	if err := e.svc.SetMute(e.ctx, c, room.ID, true); err != nil {
		t.Fatal(err)
	}
	e.send(a, room.ID, fakeText)
	e.send(a, room.ID, fakeText)
	e.runTimers()
	pushes := e.outbox(model.EventDeliveryRequested)
	if len(pushes) != 1 {
		t.Fatalf("pushes %v", pushes)
	}
	p := pushes[0]
	if p["template"] != model.TemplateNewMessage || p["class"] != model.ClassNormal || p["recipient"].(map[string]any)["member_id"] != b.MemberID.String() ||
		p["params"].(map[string]any)["room_id"] != room.ID.String() || len(p["params"].(map[string]any)) != 1 {
		t.Fatalf("push %v", p)
	}
	// Connected members are not pushed.
	e.clock.Advance(11 * time.Minute)
	if err := e.svc.TouchPresence(e.ctx, b.MemberID, []uuid.UUID{room.ID}, true); err != nil {
		t.Fatal(err)
	}
	e.send(a, room.ID, fakeText)
	e.runTimers()
	if n := len(e.outbox(model.EventDeliveryRequested)); n != 1 {
		t.Fatalf("pushed a connected member: %d", n)
	}
	if err := e.svc.TouchPresence(e.ctx, b.MemberID, []uuid.UUID{room.ID}, false); err != nil {
		t.Fatal(err)
	}
	e.send(a, room.ID, fakeText)
	e.runTimers()
	if n := len(e.outbox(model.EventDeliveryRequested)); n != 2 {
		t.Fatalf("pushes after disconnect: %d", n)
	}
}

func TestMentorLimitsPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	other := e.topicRoom("hearing")
	mentor, a := e.member(true, nil), e.member(true, nil)
	e.join(mentor, room.ID)
	e.join(a, room.ID)
	msg := e.send(a, room.ID, fakeText)

	hide := model.Moderation{Action: model.ActionHideMessage, MessageID: &msg.Message.ID}
	if err := e.svc.ModerateRoom(e.ctx, mentor, room.ID, hide); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unassigned mentor: %v", err)
	}
	// A caller who may not moderate learns nothing about unknown rooms:
	// forbidden, not "not found".
	if err := e.svc.ModerateRoom(e.ctx, a, uuid.New(), hide); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("member on unknown room: %v", err)
	}
	e.members.SetMentor(mentor.MemberID, true)
	if err := e.svc.AssignMentor(e.ctx, mentor, room.ID, mentor.MemberID, true); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("member assigned a mentor: %v", err)
	}
	if err := e.svc.AssignMentor(e.ctx, staff("psychologist"), room.ID, mentor.MemberID, true); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("psychologist assigned a mentor: %v", err)
	}
	if err := e.svc.AssignMentor(e.ctx, staff("moderator"), room.ID, mentor.MemberID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ModerateRoom(e.ctx, mentor, room.ID, hide); err != nil {
		t.Fatal(err)
	}
	if v := e.list(a, room.ID); v[0].Status != model.StatusHidden || v[0].Text != nil {
		t.Fatalf("hidden %+v", v[0])
	}
	restore := model.Moderation{Action: model.ActionRestoreMessage, MessageID: &msg.Message.ID}
	if err := e.svc.ModerateRoom(e.ctx, mentor, room.ID, restore); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ModerateRoom(e.ctx, mentor, room.ID, model.Moderation{Action: model.ActionMute1h, MemberID: &a.MemberID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Send(e.ctx, a, room.ID, uuid.Must(uuid.NewV7()), fakeText); !errors.Is(err, model.ErrBanned) {
		t.Fatalf("muted member sent: %v", err)
	}
	e.clock.Advance(61 * time.Minute)
	e.send(a, room.ID, fakeText)
	slow := 30
	for _, m := range []model.Moderation{
		{Action: model.ActionBan, MemberID: &a.MemberID},
		{Action: model.ActionUnban, MemberID: &a.MemberID},
		{Action: model.ActionSetSlowMode, SlowModeSeconds: &slow},
	} {
		if err := e.svc.ModerateRoom(e.ctx, mentor, room.ID, m); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("mentor %s: %v", m.Action, err)
		}
	}
	// Not a mentor of another room; role revoked → forbidden.
	if err := e.svc.ModerateRoom(e.ctx, mentor, other.ID, hide); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("mentor in another room: %v", err)
	}
	e.members.SetMentor(mentor.MemberID, false)
	if err := e.svc.ModerateRoom(e.ctx, mentor, room.ID, hide); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("revoked mentor: %v", err)
	}
	// Staff: ban removes the member and she cannot rejoin; unban lets her.
	mod := staff("moderator")
	if err := e.svc.ModerateRoom(e.ctx, mod, room.ID, model.Moderation{Action: model.ActionBan, MemberID: &a.MemberID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.JoinRoom(e.ctx, a, room.ID); !errors.Is(err, model.ErrBanned) {
		t.Fatalf("banned rejoin: %v", err)
	}
	if err := e.svc.ModerateRoom(e.ctx, mod, room.ID, model.Moderation{Action: model.ActionUnban, MemberID: &a.MemberID}); err != nil {
		t.Fatal(err)
	}
	e.join(a, room.ID)
	// A crisis-held message is never restored to the room.
	cm := e.send(a, room.ID, chattest.CrisisWord)
	if err := e.svc.ModerateRoom(e.ctx, mod, room.ID, model.Moderation{Action: model.ActionRestoreMessage, MessageID: &cm.Message.ID}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("crisis restore: %v", err)
	}
	// Every applied action is audited.
	var audited int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM platform.audit_log WHERE action LIKE 'chat.%'`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 6 { // assign, hide, restore, mute, ban, unban
		t.Fatalf("audit rows %d", audited)
	}
	var mentorRows int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM platform.audit_log WHERE actor_role = 'mentor' AND actor_id = $1`, mentor.MemberID).Scan(&mentorRows); err != nil || mentorRows != 3 {
		t.Fatalf("mentor audit rows %d %v", mentorRows, err)
	}
}

func TestRetentionPurgeWithReportExemptionPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	group := e.topicRoom("autism")
	a, b := e.member(true, nil), e.member(true, nil)
	e.join(a, group.ID)
	e.join(b, group.ID)
	direct, err := e.svc.OpenDirectRoom(e.ctx, uuid.New(), model.OriginMatch, uuid.New(), a.MemberID, b.MemberID)
	if err != nil {
		t.Fatal(err)
	}
	gPlain := e.send(a, group.ID, fakeText)
	gReported := e.send(a, group.ID, fakeText)
	gHold := e.send(a, group.ID, fakeText)
	dPlain := e.send(a, direct.ID, fakeText)
	dReported := e.send(a, direct.ID, fakeText)
	for _, id := range []uuid.UUID{gReported.Message.ID, dReported.Message.ID} {
		if err := e.svc.ReportMessage(e.ctx, b, id, "harassment"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.outbox(model.EventMessageReported)); n != 2 {
		t.Fatalf("reported events %d", n)
	}
	if err := e.svc.ReportMessage(e.ctx, b, gReported.Message.ID, "harassment"); err != nil || len(e.outbox(model.EventMessageReported)) != 2 {
		t.Fatalf("repeated report published again: %v", err)
	}
	if err := e.svc.SetLegalHold(e.ctx, staff("moderator"), gHold.Message.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnsureRetentionTimer(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnsureRetentionTimer(e.ctx); err != nil { // idempotent per day
		t.Fatal(err)
	}
	e.runTimers() // drains pushes; retention is not due yet
	purged := func(id uuid.UUID) bool {
		var p *time.Time
		if err := e.pool.QueryRow(e.ctx, `SELECT purged_at FROM chat.messages WHERE id = $1`, id).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p != nil
	}
	if purged(dPlain.Message.ID) {
		t.Fatal("purged too early")
	}
	e.clock.Advance(91 * 24 * time.Hour)
	e.runTimers()
	if !purged(dPlain.Message.ID) || purged(dReported.Message.ID) || purged(gPlain.Message.ID) {
		t.Fatal("90-day direct retention wrong")
	}
	e.clock.Advance(275 * 24 * time.Hour)
	e.runTimers()
	if !purged(gPlain.Message.ID) || purged(gReported.Message.ID) || purged(gHold.Message.ID) || purged(dReported.Message.ID) {
		t.Fatal("365-day group retention or exemptions wrong")
	}
	// Closing the reports lets the next sweep purge.
	if err := e.svc.CloseReports(e.ctx, staff("moderator"), gReported.Message.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(24 * time.Hour)
	e.runTimers()
	if !purged(gReported.Message.ID) || purged(gHold.Message.ID) {
		t.Fatal("closed report not purged or legal hold purged")
	}
	// A purged message reads as deleted and keeps its sequence.
	v := e.list(b, group.ID)
	if len(v) != 3 || v[0].Status != model.StatusDeleted || v[0].Text != nil || v[0].Seq != gPlain.Message.Seq {
		t.Fatalf("purged view %+v", v)
	}
	var timersPending int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM platform.timers WHERE kind = $1 AND done_at IS NULL`, model.TimerRetention).Scan(&timersPending); err != nil || timersPending != 1 {
		t.Fatalf("next retention timer: %d %v", timersPending, err)
	}
}

func TestShreddedAuthorReadsDeletedPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	a, b := e.member(true, nil), e.member(true, nil)
	e.join(a, room.ID)
	e.join(b, room.ID)
	e.send(a, room.ID, fakeText)
	e.sealer.Shred(a.MemberID)
	v := e.list(b, room.ID)
	if len(v) != 1 || v[0].Status != model.StatusDeleted || v[0].Text != nil {
		t.Fatalf("shredded view %+v", v)
	}
}

func TestRoomsJoinAndListPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	d1, d2 := uuid.New(), uuid.New()
	r1, err := e.svc.EnsureDistrictRoom(e.ctx, d1, "chat.room.district.demo_district_a")
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.EnsureDistrictRoom(e.ctx, d1, "chat.room.district.demo_district_a")
	if err != nil || again.ID != r1.ID {
		t.Fatalf("ensure not idempotent: %v", err)
	}
	r2, _ := e.svc.EnsureDistrictRoom(e.ctx, d2, "chat.room.district.demo_district_b")
	topic := e.topicRoom("autism")
	m := e.member(true, &d1)
	unverified := e.member(false, &d1)

	f := false
	joinable, err := e.svc.ListRooms(e.ctx, m, "", &f)
	if err != nil || len(joinable) != 2 {
		t.Fatalf("joinable %+v %v", joinable, err)
	}
	if v, _ := e.svc.ListRooms(e.ctx, unverified, "", &f); len(v) != 0 {
		t.Fatalf("unverified joinable %+v", v)
	}
	if _, err := e.svc.JoinRoom(e.ctx, unverified, topic.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unverified join: %v", err)
	}
	if _, err := e.svc.JoinRoom(e.ctx, m, r2.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("other district: %v", err)
	}
	e.join(m, r1.ID)
	e.join(m, r1.ID) // idempotent
	tr := true
	mine, _ := e.svc.ListRooms(e.ctx, m, "", &tr)
	if len(mine) != 1 || mine[0].ID != r1.ID || !mine[0].Joined || mine[0].Title != "chat.room.district.demo_district_a" {
		t.Fatalf("mine %+v", mine)
	}
	if err := e.svc.LeaveRoom(e.ctx, m, r1.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.LeaveRoom(e.ctx, m, r1.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CanSubscribe(e.ctx, m, r1.ID); !errors.Is(err, model.ErrNotMember) {
		t.Fatalf("left member subscribes: %v", err)
	}
	// Direct rooms: only their two members; title is the other's first name.
	b, outsider := e.member(true, nil), e.member(true, nil)
	dr, err := e.svc.OpenDirectRoom(e.ctx, uuid.New(), model.OriginSos, uuid.New(), m.MemberID, b.MemberID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.JoinRoom(e.ctx, outsider, dr.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("outsider joined direct: %v", err)
	}
	if err := e.svc.CanSubscribe(e.ctx, outsider, dr.ID); !errors.Is(err, model.ErrNotMember) {
		t.Fatalf("outsider subscribes to direct: %v", err)
	}
	direct, _ := e.svc.ListRooms(e.ctx, m, model.KindDirect, nil)
	if len(direct) != 1 || direct[0].Title != fakeName || direct[0].Kind != model.KindDirect {
		t.Fatalf("direct list %+v", direct)
	}
}

func TestDirectRoomFromEachOriginOncePostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	a, b := uuid.New(), uuid.New()
	events := []struct {
		typ  string
		data any
	}{
		{model.EventMatchAccepted, model.MatchAcceptedData{MatchID: uuid.New(), MemberA: a, MemberB: b}},
		{model.EventSosResponseAccepted, model.SosResponseAcceptedData{SosID: uuid.New(), AuthorID: b, ResponderID: a}},
		{model.EventContactAccepted, model.ContactAcceptedData{ListingID: uuid.New(), AuthorID: a, RequesterID: b}},
	}
	for _, ev := range events {
		data, _ := json.Marshal(ev.data)
		eventID := uuid.New()
		for range 3 { // redelivery of the same event
			if err := e.svc.HandleRoomEvent(e.ctx, eventID, ev.typ, data); err != nil {
				t.Fatal(err)
			}
		}
		// The origin re-published under a new event id: still one room.
		if err := e.svc.HandleRoomEvent(e.ctx, uuid.New(), ev.typ, data); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := e.pool.Query(e.ctx, `SELECT origin, count(*) FROM chat.rooms WHERE kind = 'direct' GROUP BY origin`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var o string
		var n int
		if err := rows.Scan(&o, &n); err != nil {
			t.Fatal(err)
		}
		got[o] = n
	}
	if len(got) != 3 || got[model.OriginMatch] != 1 || got[model.OriginSos] != 1 || got[model.OriginListing] != 1 {
		t.Fatalf("rooms per origin %v", got)
	}
	var members int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM chat.room_members`).Scan(&members); err != nil || members != 6 {
		t.Fatalf("room members %d %v", members, err)
	}
	if err := e.svc.HandleRoomEvent(e.ctx, uuid.New(), model.EventMatchAccepted, []byte(`{"match_id":"`+uuid.NewString()+`","member_a":"`+a.String()+`","member_b":"`+a.String()+`"}`)); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("self pair: %v", err)
	}
	if err := e.svc.HandleRoomEvent(e.ctx, uuid.New(), "circle.Other", []byte(`{}`)); err != nil {
		t.Fatalf("unknown type: %v", err)
	}
}

// TestEnsureGroupRoomsPostgres: concurrent ensures of one district make one
// room; EnsureGroupRooms creates only what is missing, keeps existing ids and
// titles, and leaves the room of a district it is no longer given.
func TestEnsureGroupRoomsPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	d1, d2, d3 := uuid.New(), uuid.New(), uuid.New()

	const workers = 16
	ids := make([]uuid.UUID, workers)
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.svc.EnsureDistrictRoom(e.ctx, d1, "chat.room.district.demo_district_a")
			if err != nil {
				errs <- err
				return
			}
			ids[i] = r.ID
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ensure: %v", err)
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("concurrent ensures returned different rooms: %v", ids)
		}
	}
	countDistrict := func(d uuid.UUID) int {
		var n int
		if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM chat.rooms WHERE kind = 'district' AND district_id = $1`, d).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := countDistrict(d1); n != 1 {
		t.Fatalf("district rooms after concurrent ensures = %d, want 1", n)
	}

	refs := []service.DistrictRoomRef{
		{DistrictID: d1, TitleKey: "chat.room.district.other_title"},
		{DistrictID: d2, TitleKey: "chat.room.district.demo_district_b"},
		{DistrictID: d3, TitleKey: "chat.room.district.demo_district_c"},
	}
	topics := []service.TopicRoomRef{{Code: "autism", TitleKey: "chat.room.topic.autism"}}
	res, err := e.svc.EnsureGroupRooms(e.ctx, refs, topics)
	if err != nil || res != (service.EnsureGroupRoomsResult{DistrictsCreated: 2, TopicsCreated: 1}) {
		t.Fatalf("first ensure = %+v, %v", res, err)
	}
	kept, err := e.svc.EnsureDistrictRoom(e.ctx, d1, "chat.room.district.demo_district_a")
	if err != nil || kept.ID != ids[0] || kept.TitleKey != "chat.room.district.demo_district_a" {
		t.Fatalf("existing room changed: %+v, %v", kept, err)
	}
	// Concurrent runs of the whole step (two migrate jobs) create nothing twice.
	var wg2 sync.WaitGroup
	results := make([]service.EnsureGroupRoomsResult, 4)
	runErrs := make([]error, 4)
	for i := range results {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			results[i], runErrs[i] = e.svc.EnsureGroupRooms(e.ctx, refs, topics)
		}()
	}
	wg2.Wait()
	for i := range results {
		if runErrs[i] != nil || results[i] != (service.EnsureGroupRoomsResult{}) {
			t.Fatalf("repeated ensure %d = %+v, %v; want nothing created", i, results[i], runErrs[i])
		}
	}
	// d3 is deactivated: no longer passed, its room stays.
	if res, err := e.svc.EnsureGroupRooms(e.ctx, refs[:2], topics); err != nil || res != (service.EnsureGroupRoomsResult{}) {
		t.Fatalf("ensure without d3 = %+v, %v", res, err)
	}
	if n := countDistrict(d3); n != 1 {
		t.Fatalf("deactivated district rooms = %d, want 1", n)
	}
	// Invalid input fails the step instead of skipping a room silently.
	if _, err := e.svc.EnsureGroupRooms(e.ctx, []service.DistrictRoomRef{{DistrictID: uuid.Nil, TitleKey: "chat.room.district.x"}}, nil); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("nil district: %v", err)
	}
	if _, err := e.svc.EnsureGroupRooms(e.ctx, nil, []service.TopicRoomRef{{Code: "Bad-Code", TitleKey: "chat.room.topic.x"}}); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad topic code: %v", err)
	}
}

func TestRoomOpenerConsumerJetStream(t *testing.T) {
	e := newEnv(t, service.Settings{})
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
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- (&adapter.RoomOpenerConsumer{JS: js, Stream: "EVENTS", Service: e.svc}).Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	data, _ := json.Marshal(model.SosResponseAcceptedData{SosID: uuid.New(), AuthorID: uuid.New(), ResponderID: uuid.New()})
	env, _ := json.Marshal(outbox.Envelope{ID: uuid.New(), Type: model.EventSosResponseAccepted, Version: 1, OccurredAt: e.clock.Now(), Data: data})
	// The same envelope twice under different broker message ids (JetStream
	// dedup does not catch it): platform.processed_events must.
	for range 2 {
		if _, err := js.Publish(e.ctx, outbox.Subject(model.EventSosResponseAccepted), env, jetstream.WithMsgID(uuid.NewString())); err != nil {
			t.Fatal(err)
		}
	}
	var rooms, processed int
	for range 200 {
		if err := e.pool.QueryRow(e.ctx, `SELECT (SELECT count(*) FROM chat.rooms), (SELECT count(*) FROM platform.processed_events WHERE consumer = $1)`,
			model.ConsumerDirectRooms).Scan(&rooms, &processed); err != nil {
			t.Fatal(err)
		}
		if rooms == 1 && processed == 1 {
			info, err := js.Consumer(e.ctx, "EVENTS", "chat-direct-rooms")
			if err == nil {
				if ci, err := info.Info(e.ctx); err == nil && ci.NumAckPending == 0 && ci.NumPending == 0 {
					return
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("rooms=%d processed=%d", rooms, processed)
}

func TestStaffListRoomsPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	mentor, a, b := e.member(true, nil), e.member(true, nil), e.member(true, nil)
	e.join(mentor, room.ID)
	e.join(a, room.ID)
	if _, err := e.svc.OpenDirectRoom(e.ctx, uuid.New(), model.OriginSos, uuid.New(), a.MemberID, b.MemberID); err != nil {
		t.Fatal(err)
	}
	e.members.SetMentor(mentor.MemberID, true)
	if err := e.svc.AssignMentor(e.ctx, staff("moderator"), room.ID, mentor.MemberID, true); err != nil {
		t.Fatal(err)
	}

	for _, p := range []auth.Principal{a, staff("psychologist"), staff("lawyer"), staff()} {
		if _, err := e.svc.StaffListRooms(e.ctx, p); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("%+v listed rooms: %v", p.Roles, err)
		}
	}
	rooms, err := e.svc.StaffListRooms(e.ctx, staff("admin"))
	if err != nil {
		t.Fatal(err)
	}
	var got *model.StaffRoom
	for i := range rooms {
		if rooms[i].Kind == model.KindDirect {
			t.Fatalf("direct room listed to staff: %+v", rooms[i])
		}
		if rooms[i].ID == room.ID {
			got = &rooms[i]
		}
	}
	if got == nil || got.MemberCount != 2 || got.Title != room.TitleKey ||
		len(got.Mentors) != 1 || got.Mentors[0].MemberID != mentor.MemberID || got.Mentors[0].DisplayName != fakeName {
		t.Fatalf("staff room = %+v", got)
	}
	if err := e.svc.AssignMentor(e.ctx, staff("moderator"), room.ID, mentor.MemberID, false); err != nil {
		t.Fatal(err)
	}
	rooms, _ = e.svc.StaffListRooms(e.ctx, staff("moderator"))
	for _, r := range rooms {
		if r.ID == room.ID && len(r.Mentors) != 0 {
			t.Fatalf("removed mentor still listed: %+v", r.Mentors)
		}
	}
}

func TestMentorRoleRevokedConsumerJetStream(t *testing.T) {
	e := newEnv(t, service.Settings{})
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

	autism, hearing := e.topicRoom("autism"), e.topicRoom("hearing")
	mentor, other := e.member(true, nil), e.member(true, nil)
	mod := staff("moderator")
	for _, p := range []auth.Principal{mentor, other} {
		e.members.SetMentor(p.MemberID, true)
	}
	for _, a := range []struct {
		room   uuid.UUID
		member uuid.UUID
	}{{autism.ID, mentor.MemberID}, {hearing.ID, mentor.MemberID}, {autism.ID, other.MemberID}} {
		if err := e.svc.AssignMentor(e.ctx, mod, a.room, a.member, true); err != nil {
			t.Fatal(err)
		}
	}
	// Membership takes the role, then publishes the event.
	e.members.SetMentor(mentor.MemberID, false)

	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- (&adapter.MentorRoleConsumer{JS: js, Stream: "EVENTS", Service: e.svc}).Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	revokeID := uuid.New()
	publish := func(id uuid.UUID, mentorFlag bool) {
		t.Helper()
		data, _ := json.Marshal(model.MentorRoleChangedData{MemberID: mentor.MemberID, Mentor: mentorFlag})
		env, _ := json.Marshal(outbox.Envelope{ID: id, Type: model.EventMentorRoleChanged, Version: 1, OccurredAt: e.clock.Now(), Data: data})
		if _, err := js.Publish(e.ctx, outbox.Subject(model.EventMentorRoleChanged), env, jetstream.WithMsgID(uuid.NewString())); err != nil {
			t.Fatal(err)
		}
	}
	// The same envelope twice under different broker message ids (JetStream
	// dedup does not catch it): platform.processed_events must.
	publish(revokeID, false)
	publish(revokeID, false)

	settled := func() bool {
		info, err := js.Consumer(e.ctx, "EVENTS", "chat-mentor-role-changed")
		if err != nil {
			return false
		}
		ci, err := info.Info(e.ctx)
		return err == nil && ci.NumAckPending == 0 && ci.NumPending == 0
	}
	var processed int
	for range 200 {
		if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM platform.processed_events WHERE consumer = $1`,
			model.ConsumerMentorRoleChanged).Scan(&processed); err != nil {
			t.Fatal(err)
		}
		if processed == 1 && settled() {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if processed != 1 || !settled() {
		t.Fatalf("processed=%d settled=%v", processed, settled())
	}

	mentorsOf := func() map[uuid.UUID][]uuid.UUID {
		t.Helper()
		rooms, err := e.svc.StaffListRooms(e.ctx, mod)
		if err != nil {
			t.Fatal(err)
		}
		out := map[uuid.UUID][]uuid.UUID{}
		for _, r := range rooms {
			for _, m := range r.Mentors {
				out[r.ID] = append(out[r.ID], m.MemberID)
			}
		}
		return out
	}
	got := mentorsOf()
	if len(got[hearing.ID]) != 0 || len(got[autism.ID]) != 1 || got[autism.ID][0] != other.MemberID {
		t.Fatalf("mentors after revoke = %v", got)
	}
	var removedAt int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM chat.room_mentors WHERE member_id = $1 AND removed_at IS NOT NULL`,
		mentor.MemberID).Scan(&removedAt); err != nil || removedAt != 2 {
		t.Fatalf("rows kept with removed_at: %d %v", removedAt, err)
	}
	systemAudits := func() int {
		t.Helper()
		var n int
		if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM platform.audit_log
			WHERE action = $1 AND actor_role = 'system' AND actor_id = $2 AND object_id = $3 AND reason = $4`,
			model.AuditMentorRemove, revokeID, mentor.MemberID, model.AuditReasonMentorRevoked).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := systemAudits(); n != 2 {
		t.Fatalf("system audit rows %d, want one per room", n)
	}

	// Staff give the role back and reassign one room; granting assigns
	// nothing by itself, and replaying the old revoke is a no-op.
	e.members.SetMentor(mentor.MemberID, true)
	if err := e.svc.ApplyMentorRoleChanged(e.ctx, uuid.New(), model.MentorRoleChangedData{MemberID: mentor.MemberID, Mentor: true}); err != nil {
		t.Fatal(err)
	}
	if got := mentorsOf(); len(got[hearing.ID]) != 0 {
		t.Fatalf("grant assigned a room: %v", got)
	}
	if err := e.svc.AssignMentor(e.ctx, mod, hearing.ID, mentor.MemberID, true); err != nil {
		t.Fatal(err)
	}
	publish(revokeID, false)
	for range 200 {
		if settled() {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := e.svc.ApplyMentorRoleChanged(e.ctx, revokeID, model.MentorRoleChangedData{MemberID: mentor.MemberID}); err != nil {
		t.Fatal(err)
	}
	if got := mentorsOf(); len(got[hearing.ID]) != 1 || got[hearing.ID][0] != mentor.MemberID {
		t.Fatalf("replayed revoke removed a new assignment: %v", got)
	}
	if n := systemAudits(); n != 2 {
		t.Fatalf("replay audited again: %d", n)
	}
}
