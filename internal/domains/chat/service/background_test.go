package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/auth"
)

func (e *env) push(messageID uuid.UUID) error {
	return e.inTx(func(tx port.Tx) error { return e.svc.HandlePush(e.ctx, tx, messageID) })
}

func pushedTo(evs []model.Event) map[uuid.UUID]model.DeliveryRequestedData {
	out := map[uuid.UUID]model.DeliveryRequestedData{}
	for _, ev := range evs {
		d := ev.Data.(model.DeliveryRequestedData)
		out[*d.Recipient.MemberID] = d
	}
	return out
}

func TestHandlePushRecipients(t *testing.T) {
	e := newEnv(t, service.Settings{PushInterval: 10 * time.Minute})
	room := e.topicRoom("autism")
	author, plain, muted, online, blocker, left := e.member(true, nil), e.member(true, nil), e.member(true, nil),
		e.member(true, nil), e.member(true, nil), e.member(true, nil)
	for _, p := range []auth.Principal{author, plain, muted, online, blocker, left} {
		e.join(p, room.ID)
	}
	if err := e.svc.SetMute(e.ctx, muted, room.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.TouchPresence(e.ctx, online.MemberID, []uuid.UUID{room.ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.LeaveRoom(e.ctx, left, room.ID); err != nil {
		t.Fatal(err)
	}
	e.members.Block(blocker.MemberID, author.MemberID)

	m := e.send(author, room.ID, fakeText).Message
	if err := e.push(m.ID); err != nil {
		t.Fatal(err)
	}
	got := pushedTo(e.store.EventsOf(model.EventDeliveryRequested))
	if len(got) != 1 {
		t.Fatalf("pushed to %d members, want only the plain one: %+v", len(got), got)
	}
	d, ok := got[plain.MemberID]
	if !ok || d.Template != model.TemplateNewMessage || d.Class != model.ClassNormal || d.Params["room_id"] != room.ID.String() || len(d.Params) != 1 {
		t.Fatalf("push %+v: content-free, room id only", d)
	}
	// Deterministic request id: a retried timer cannot push twice.
	if d.RequestID != model.NewMessagePush(m.ID, room.ID, plain.MemberID).Data.(model.DeliveryRequestedData).RequestID {
		t.Fatal("request id is not deterministic")
	}

	// Throttle: a second message within the interval is not pushed again.
	e.clock.Advance(time.Minute)
	m2 := e.send(author, room.ID, fakeText).Message
	if err := e.push(m2.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(e.store.EventsOf(model.EventDeliveryRequested)); n != 1 {
		t.Fatalf("throttle: %d pushes", n)
	}
	e.clock.Advance(10 * time.Minute)
	m3 := e.send(author, room.ID, fakeText).Message
	if err := e.push(m3.ID); err != nil {
		t.Fatal(err)
	}
	// After the interval the plain member is pushed again; the online one's
	// presence has expired by now too.
	if n := len(e.store.EventsOf(model.EventDeliveryRequested)); n != 3 {
		t.Fatalf("after the interval: %d pushes, want 3", n)
	}
}

func TestHandlePushSkipsAndFailures(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	author, reader := e.member(true, nil), e.member(true, nil)
	e.join(author, room.ID)
	e.join(reader, room.ID)

	if err := e.push(uuid.New()); err != nil {
		t.Fatalf("a missing message is done, not retried: %v", err)
	}
	held := e.send(author, room.ID, "fake www.example.invalid").Message
	if err := e.push(held.ID); err != nil {
		t.Fatal(err)
	}
	if len(e.store.EventsOf(model.EventDeliveryRequested)) != 0 {
		t.Fatal("held message pushed")
	}

	m := e.send(author, room.ID, fakeText).Message
	e.members.Err = errors.New("membership down")
	if err := e.push(m.ID); err == nil {
		t.Fatal("pushed without block information")
	}
	e.members.Err = nil
	for _, method := range []string{"GetMessage", "ListPushCandidates", "Publish", "MarkPushed"} {
		e.store.FailOn(method, errors.New("database down"))
		if err := e.push(m.ID); err == nil {
			t.Fatalf("%s failure not returned", method)
		}
		e.store.FailOn(method, nil)
	}
	if len(e.store.EventsOf(model.EventDeliveryRequested)) != 0 {
		t.Fatal("a failed run left a push")
	}
}

func TestHandleCrisisEscalationSkips(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	author := e.member(true, nil)
	e.join(author, room.ID)
	run := func(id uuid.UUID) error {
		return e.inTx(func(tx port.Tx) error { return e.svc.HandleCrisisEscalation(e.ctx, tx, id) })
	}
	if err := run(uuid.New()); err != nil {
		t.Fatalf("missing message: %v", err)
	}
	normal := e.send(author, room.ID, fakeText).Message
	if err := run(normal.ID); err != nil || e.crisis.Calls != 0 {
		t.Fatalf("non-crisis message escalated: %v, %d calls", err, e.crisis.Calls)
	}
	e.store.FailOn("GetMessage", errors.New("database down"))
	if err := run(normal.ID); err == nil {
		t.Fatal("store failure not returned")
	}
	e.store.FailOn("GetMessage", nil)

	// Without the crisis port the timer fails, so it is retried, never dropped.
	e.crisis.SetErr(errors.New("safety down"))
	crisis, _ := e.svc.Send(e.ctx, author, room.ID, clientID(), "fake fakecrisisword")
	e.svc.Crisis = nil
	if err := run(crisis.Message.ID); err == nil {
		t.Fatal("crisis timer done without a crisis port")
	}
}

func TestRetention(t *testing.T) {
	e := newEnv(t, service.Settings{RetentionGroup: 30 * 24 * time.Hour, RetentionDirect: 7 * 24 * time.Hour, RetentionAt: 2 * time.Hour, RateLimit: 100})
	group := e.topicRoom("autism")
	a, b := e.member(true, nil), e.member(true, nil)
	e.join(a, group.ID)
	direct := e.directRoom(model.OriginSos, a, b)

	old := e.send(a, group.ID, fakeText).Message
	held := e.send(a, group.ID, fakeText).Message
	reported := e.send(a, group.ID, fakeText).Message
	closed := e.send(a, group.ID, fakeText).Message
	oldDirect := e.send(a, direct.ID, fakeText).Message
	recentDirect := e.send(a, direct.ID, fakeText).Message
	recentGroup := e.send(a, group.ID, fakeText).Message
	now := e.clock.Now()
	for _, id := range []uuid.UUID{old.ID, held.ID, reported.ID, closed.ID} {
		e.store.SetCreatedAt(id, now.Add(-31*24*time.Hour))
	}
	e.store.SetCreatedAt(oldDirect.ID, now.Add(-8*24*time.Hour))
	e.store.SetCreatedAt(recentDirect.ID, now.Add(-6*24*time.Hour))
	e.store.SetCreatedAt(recentGroup.ID, now.Add(-29*24*time.Hour))

	mod := staff("admin")
	if err := e.svc.SetLegalHold(e.ctx, mod, held.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{reported.ID, closed.ID} {
		if err := e.svc.ReportMessage(e.ctx, b, id, "spam"); !errors.Is(err, model.ErrNotFound) {
			// b is not in the group room: join first.
			t.Fatalf("report by a non-member: %v", err)
		}
	}
	e.join(b, group.ID)
	for _, id := range []uuid.UUID{reported.ID, closed.ID} {
		if err := e.svc.ReportMessage(e.ctx, b, id, "spam"); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.CloseReports(e.ctx, mod, closed.ID); err != nil {
		t.Fatal(err)
	}

	if err := e.inTx(func(tx port.Tx) error { return e.svc.HandleRetention(e.ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	want := map[uuid.UUID]bool{ // purged?
		old.ID: true, held.ID: false, reported.ID: false, closed.ID: true,
		oldDirect.ID: true, recentDirect.ID: false, recentGroup.ID: false,
	}
	for id, purged := range want {
		m, _ := e.store.Message(id)
		if (m.PurgedAt != nil) != purged || (m.Text == nil) != purged {
			t.Fatalf("message %v purged=%v, want %v", id, m.PurgedAt != nil, purged)
		}
	}
	// A purged message stays as a "deleted" tombstone in the history.
	vs := e.list(b, group.ID)
	if len(vs) != 5 || vs[0].ID != old.ID || vs[0].Status != model.StatusDeleted || vs[0].Text != nil {
		t.Fatalf("history after purge: %+v", vs)
	}
	if text, err := e.svc.ModerationSummary(e.ctx, old.ID); !errors.Is(err, model.ErrNotFound) || text != "" {
		t.Fatalf("summary of a purged message: %q %v", text, err)
	}

	// The next run is scheduled for tomorrow 02:00 UTC, once.
	next := e.store.ActiveTimers(model.TimerRetention)
	wantAt := time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC)
	if len(next) != 1 || !next[0].DueAt.Equal(wantAt) || next[0].SubjectID != model.RetentionSubject {
		t.Fatalf("next retention timer %+v", next)
	}
	if err := e.svc.EnsureRetentionTimer(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.store.ActiveTimers(model.TimerRetention)); n != 1 {
		t.Fatalf("retention timers %d after a second ensure", n)
	}

	e.store.FailOn("PurgeExpired", errors.New("database down"))
	if err := e.inTx(func(tx port.Tx) error { return e.svc.HandleRetention(e.ctx, tx) }); err == nil {
		t.Fatal("purge failure not returned")
	}
}

func TestRetentionPagesThroughLargeBacklog(t *testing.T) {
	e := newEnv(t, service.Settings{RateLimit: 5000})
	room := e.topicRoom("autism")
	a := e.member(true, nil)
	e.join(a, room.ID)
	const n = 1005 // more than one purge page (1000)
	var created []uuid.UUID
	for range n {
		created = append(created, e.send(a, room.ID, fakeText).Message.ID)
	}
	old := e.clock.Now().Add(-service.DefaultRetentionGroup - time.Hour)
	for _, id := range created {
		e.store.SetCreatedAt(id, old)
	}
	if err := e.inTx(func(tx port.Tx) error { return e.svc.HandleRetention(e.ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	for _, m := range e.store.Messages(room.ID) {
		if m.PurgedAt == nil {
			t.Fatalf("message %d not purged", m.Seq)
		}
	}
	// Default sweep time: 03:00 UTC.
	next := e.store.ActiveTimers(model.TimerRetention)
	if len(next) != 1 || next[0].DueAt.Hour() != 3 {
		t.Fatalf("next retention %+v", next)
	}
}
