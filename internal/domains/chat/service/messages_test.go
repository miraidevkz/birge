package service_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/chattest"
	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/auth"
)

func TestSendValidation(t *testing.T) {
	e := newEnv(t, service.Settings{})
	topic := e.topicRoom("autism")
	mother, outsider := e.member(true, nil), e.member(true, nil)
	e.join(mother, topic.ID)

	cases := []struct {
		name   string
		p      auth.Principal
		room   uuid.UUID
		client uuid.UUID
		text   string
		want   error
	}{
		{"staff cannot post", staff("admin"), topic.ID, clientID(), fakeText, model.ErrUnauthorized},
		{"anonymous", auth.Principal{}, topic.ID, clientID(), fakeText, model.ErrUnauthorized},
		{"nil client id", mother, topic.ID, uuid.Nil, fakeText, model.ErrInvalidInput},
		{"client id not v7", mother, topic.ID, uuid.New(), fakeText, model.ErrInvalidInput},
		{"blank text", mother, topic.ID, clientID(), " \n\t ", model.ErrInvalidInput},
		{"too long", mother, topic.ID, clientID(), strings.Repeat("я", model.MaxTextRunes+1), model.ErrInvalidInput},
		{"invalid utf-8", mother, topic.ID, clientID(), "fake \xff", model.ErrInvalidInput},
		{"not a member", outsider, topic.ID, clientID(), fakeText, model.ErrNotMember},
		{"unknown room", mother, uuid.New(), clientID(), fakeText, model.ErrNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := e.svc.Send(e.ctx, c.p, c.room, c.client, c.text)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			if res.Crisis != nil {
				t.Fatal("crisis notice on a non-crisis failure")
			}
		})
	}
	if res, err := e.svc.Send(e.ctx, mother, topic.ID, clientID(), strings.Repeat("я", model.MaxTextRunes)); err != nil || res.Message.Text == nil {
		t.Fatalf("exactly the limit must pass: %v", err)
	}
	if n := len(e.store.Messages(topic.ID)); n != 1 {
		t.Fatalf("rejected sends stored messages: %d", n)
	}

	// Without the crisis detector nothing is sent: the check is never skipped.
	e.svc.Crisis = nil
	if _, err := e.svc.Send(e.ctx, mother, topic.ID, clientID(), fakeText); err == nil {
		t.Fatal("send without a crisis detector")
	}
}

func TestSendVisibleMessage(t *testing.T) {
	e := newEnv(t, service.Settings{})
	topic := e.topicRoom("autism")
	author, reader := e.member(true, nil), e.member(true, nil)
	e.join(author, topic.ID)
	e.join(reader, topic.ID)

	res := e.send(author, topic.ID, fakeText)
	v := res.Message
	if res.Crisis != nil || res.Replayed || v.Status != model.StatusVisible || !v.Mine || v.Seq != 1 ||
		v.Text == nil || *v.Text != fakeText || v.AuthorName != fakeName {
		t.Fatalf("result %+v", res)
	}
	stored, _ := e.store.Message(v.ID)
	if strings.Contains(string(stored.Text), "fake hello") {
		t.Fatal("text stored in plaintext")
	}
	if got := e.store.ActiveTimers(model.TimerPush); len(got) != 1 || got[0].SubjectID != v.ID {
		t.Fatalf("push timer %+v", got)
	}
	if len(e.store.EventsOf(model.EventMessageHeld)) != 0 {
		t.Fatal("visible message published as held")
	}
	notices := e.bus.Notices()
	if len(notices) != 1 || notices[0].Kind != model.NoticeNew || *notices[0].MessageID != v.ID || notices[0].Seq != 1 {
		t.Fatalf("notices %+v", notices)
	}
	if locks := e.store.Locks(); len(locks) == 0 || locks[0] != model.LockSend+"|"+author.MemberID.String() {
		t.Fatalf("send did not take the author's lock first: %v", locks)
	}

	got := e.list(reader, topic.ID)
	if len(got) != 1 || got[0].Mine || got[0].Text == nil || *got[0].Text != fakeText {
		t.Fatalf("reader view %+v", got)
	}
	if res2 := e.send(reader, topic.ID, fakeText); res2.Message.Seq != 2 {
		t.Fatalf("second seq %d", res2.Message.Seq)
	}

	// A fan-out failure does not fail the committed send.
	e.bus.Error = errors.New("nats down")
	if _, err := e.svc.Send(e.ctx, author, topic.ID, clientID(), fakeText); err != nil {
		t.Fatalf("fan-out failure surfaced: %v", err)
	}
}

func TestSendReplayIsIdempotent(t *testing.T) {
	e := newEnv(t, service.Settings{})
	topic := e.topicRoom("autism")
	author := e.member(true, nil)
	e.join(author, topic.ID)
	cid := clientID()
	first, err := e.svc.Send(e.ctx, author, topic.ID, cid, fakeText)
	if err != nil {
		t.Fatal(err)
	}
	// The replay returns the stored message even with another text and
	// another room: the client id is the author's, not the room's.
	other := e.topicRoom("down_syndrome")
	e.join(author, other.ID)
	again, err := e.svc.Send(e.ctx, author, other.ID, cid, "fake other text")
	if err != nil {
		t.Fatal(err)
	}
	if !again.Replayed || again.Message.ID != first.Message.ID || *again.Message.Text != fakeText || again.Message.RoomID != topic.ID {
		t.Fatalf("replay %+v", again)
	}
	if len(e.store.Messages(topic.ID))+len(e.store.Messages(other.ID)) != 1 {
		t.Fatal("replay stored a second message")
	}
	if r, _ := e.store.Room(other.ID); r.LastSeq != 0 {
		t.Fatalf("replay used a sequence number: %d", r.LastSeq)
	}
	if len(e.store.ActiveTimers(model.TimerPush)) != 1 {
		t.Fatal("replay scheduled a second push")
	}

}

func TestSendFilterHolds(t *testing.T) {
	cases := []struct {
		name, text, reason string
	}{
		{"link", "fake see https://example.invalid/x", model.HoldLink},
		{"bare domain", "fake visit fakeshop.kz today", model.HoldLink},
		{"phone", "fake call +7 (700) 000-00-00", model.HoldPhone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, service.Settings{})
			topic := e.topicRoom("autism")
			author, reader := e.member(true, nil), e.member(true, nil)
			e.join(author, topic.ID)
			e.join(reader, topic.ID)
			res := e.send(author, topic.ID, c.text)
			if res.Message.Status != model.StatusHeld || res.Crisis != nil {
				t.Fatalf("result %+v", res)
			}
			held := e.store.EventsOf(model.EventMessageHeld)
			if len(held) != 1 {
				t.Fatalf("held events %d", len(held))
			}
			if d := held[0].Data.(model.MessageHeldData); d.MessageID != res.Message.ID || d.Reason != c.reason {
				t.Fatalf("held event %+v", d)
			}
			if len(e.store.ActiveTimers(model.TimerPush)) != 0 {
				t.Fatal("held message pushed")
			}
			if got := e.list(reader, topic.ID); len(got) != 0 {
				t.Fatalf("held message visible to others: %+v", got)
			}
			if got := e.list(author, topic.ID); len(got) != 1 || got[0].Status != model.StatusHeld || *got[0].Text != c.text {
				t.Fatalf("author does not see her held message: %+v", got)
			}
		})
	}
}

func TestSendNewcomerHoldOnlyInGroupRooms(t *testing.T) {
	e := newEnv(t, service.Settings{NewcomerHeld: 2})
	topic := e.topicRoom("autism")
	author, friend := e.member(true, nil), e.member(true, nil)
	e.join(author, topic.ID)
	direct := e.directRoom(model.OriginMatch, author, friend)

	if res := e.send(author, direct.ID, fakeText); res.Message.Status != model.StatusVisible {
		t.Fatalf("direct message held for a newcomer: %+v", res.Message)
	}
	var statuses []string
	for range 3 {
		statuses = append(statuses, e.send(author, topic.ID, fakeText).Message.Status)
	}
	if statuses[0] != model.StatusHeld || statuses[1] != model.StatusHeld || statuses[2] != model.StatusVisible {
		t.Fatalf("statuses %v, want held, held, visible", statuses)
	}
	held := e.store.EventsOf(model.EventMessageHeld)
	if len(held) != 2 || held[0].Data.(model.MessageHeldData).Reason != model.HoldNewcomer {
		t.Fatalf("held events %+v", held)
	}

	e.store.FailOn("CountAuthorGroupMessages", errors.New("database down"))
	if _, err := e.svc.Send(e.ctx, author, topic.ID, clientID(), fakeText); err == nil {
		t.Fatal("want store error")
	}
}

func TestSendCrisisMessage(t *testing.T) {
	e := newEnv(t, service.Settings{RateLimit: 1})
	topic := e.topicRoom("autism")
	author, reader := e.member(true, nil), e.member(true, nil)
	e.join(author, topic.ID)
	e.join(reader, topic.ID)
	mod := staff("moderator")
	// Rate limit used up, slow mode on and muted: a crisis message still goes through.
	e.send(author, topic.ID, fakeText)
	slow := 600
	if err := e.svc.ModerateRoom(e.ctx, mod, topic.ID, model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: &slow}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ModerateRoom(e.ctx, mod, topic.ID, model.Moderation{Action: model.ActionMute1h, MemberID: &author.MemberID}); err != nil {
		t.Fatal(err)
	}
	noticesBefore := len(e.bus.Notices())

	text := "fake text " + chattest.CrisisWord
	cid := clientID()
	res, err := e.svc.Send(e.ctx, author, topic.ID, cid, text)
	if err != nil {
		t.Fatal(err)
	}
	if res.Crisis == nil || len(res.Crisis.Phones) == 0 || res.Crisis.EscalationID == nil {
		t.Fatalf("crisis notice %+v", res.Crisis)
	}
	esc, _ := e.crisis.Escalation(author.MemberID)
	if *res.Crisis.EscalationID != esc || e.crisis.Sources[0] != model.EscalationSource {
		t.Fatalf("escalation %v vs %v, sources %v", *res.Crisis.EscalationID, esc, e.crisis.Sources)
	}
	stored, _ := e.store.Message(res.Message.ID)
	if stored.Status != model.StatusHeld || stored.HoldReason != model.HoldCrisis || stored.EscalationID == nil || *stored.EscalationID != esc {
		t.Fatalf("stored %+v", stored)
	}
	// The retry timer was created and then cancelled by the synchronous attempt.
	if len(e.store.Timers()) == 0 || len(e.store.ActiveTimers(model.TimerCrisisEscalation)) != 0 {
		t.Fatalf("timers %+v", e.store.Timers())
	}
	// Never in the moderation queue, never pushed, no live notice to the room.
	if len(e.store.EventsOf(model.EventMessageHeld)) != 0 || len(e.store.ActiveTimers(model.TimerPush)) != 1 {
		t.Fatal("crisis message published as held or pushed")
	}
	if len(e.bus.Notices()) != noticesBefore {
		t.Fatal("crisis message announced to the room")
	}
	if got := e.list(reader, topic.ID); len(got) != 1 {
		t.Fatalf("reader sees the crisis message: %+v", got)
	}

	// A replay carries the crisis notice again with the recorded escalation.
	again, err := e.svc.Send(e.ctx, author, topic.ID, cid, text)
	if err != nil || !again.Replayed || again.Crisis == nil || again.Crisis.EscalationID == nil || *again.Crisis.EscalationID != esc {
		t.Fatalf("replay %+v %v", again, err)
	}
}

func TestSendCrisisWhenSafetyIsDown(t *testing.T) {
	e := newEnv(t, service.Settings{})
	topic := e.topicRoom("autism")
	author := e.member(true, nil)
	e.join(author, topic.ID)
	e.crisis.SetErr(errors.New("safety down"))

	res, err := e.svc.Send(e.ctx, author, topic.ID, clientID(), "fake "+chattest.CrisisWord)
	if err != nil {
		t.Fatal(err)
	}
	if res.Crisis == nil || len(res.Crisis.Phones) == 0 || res.Crisis.EscalationID != nil {
		t.Fatalf("the phones must be shown even while safety is down: %+v", res.Crisis)
	}
	timers := e.store.ActiveTimers(model.TimerCrisisEscalation)
	if len(timers) != 1 || timers[0].SubjectID != res.Message.ID {
		t.Fatalf("retry timer %+v", timers)
	}

	// The timer retries; while safety is down it fails, so the worker backs off.
	retry := func() error {
		return e.inTx(func(tx port.Tx) error { return e.svc.HandleCrisisEscalation(e.ctx, tx, res.Message.ID) })
	}
	if err := retry(); err == nil {
		t.Fatal("retry succeeded while safety is down")
	}
	e.crisis.SetErr(nil)
	if err := retry(); err != nil {
		t.Fatal(err)
	}
	stored, _ := e.store.Message(res.Message.ID)
	esc, _ := e.crisis.Escalation(author.MemberID)
	if stored.EscalationID == nil || *stored.EscalationID != esc {
		t.Fatalf("escalation not recorded: %+v", stored)
	}
	// Once recorded, a further run opens nothing new.
	calls := e.crisis.Calls
	if err := retry(); err != nil || e.crisis.Calls != calls {
		t.Fatalf("second retry: %v, calls %d -> %d", err, calls, e.crisis.Calls)
	}
}

func TestSendCrisisEscalatedEvenWhenNotStored(t *testing.T) {
	e := newEnv(t, service.Settings{})
	topic := e.topicRoom("autism")
	outsider := e.member(true, nil)

	// Not a member: the message is refused, but the escalation is opened and
	// the notice returned next to the error.
	res, err := e.svc.Send(e.ctx, outsider, topic.ID, clientID(), "fake "+chattest.CrisisWord)
	if !errors.Is(err, model.ErrNotMember) {
		t.Fatalf("got %v", err)
	}
	if res.Crisis == nil || res.Crisis.EscalationID == nil {
		t.Fatalf("crisis notice %+v", res.Crisis)
	}

	// The author's key is gone (sealing fails): same.
	member := e.member(true, nil)
	e.join(member, topic.ID)
	e.sealer.Shred(member.MemberID)
	res, err = e.svc.Send(e.ctx, member, topic.ID, clientID(), "fake "+chattest.CrisisWord)
	if err == nil || res.Crisis == nil || res.Crisis.EscalationID == nil {
		t.Fatalf("seal failure: %+v %v", res, err)
	}

	// Database down and safety down: the phones are still returned.
	e.crisis.SetErr(errors.New("safety down"))
	other := e.member(true, nil)
	e.join(other, topic.ID)
	e.store.FailOn("InsertMessage", errors.New("database down"))
	res, err = e.svc.Send(e.ctx, other, topic.ID, clientID(), "fake "+chattest.CrisisWord)
	if err == nil || res.Crisis == nil || len(res.Crisis.Phones) == 0 || res.Crisis.EscalationID != nil {
		t.Fatalf("double outage: %+v %v", res, err)
	}

	// A non-crisis message that cannot be sealed is just an error.
	e.store.FailOn("InsertMessage", nil)
	if res, err := e.svc.Send(e.ctx, member, topic.ID, clientID(), fakeText); err == nil || res.Crisis != nil {
		t.Fatalf("seal failure of a normal message: %+v %v", res, err)
	}
}

func TestSendCrisisEscalationNotRecorded(t *testing.T) {
	e := newEnv(t, service.Settings{})
	topic := e.topicRoom("autism")
	author := e.member(true, nil)
	e.join(author, topic.ID)
	// The escalation opens but recording it fails: the timer stays active and
	// will find the escalation open (idempotent per member).
	e.store.FailOn("SetMessageEscalation", errors.New("database down"))
	res, err := e.svc.Send(e.ctx, author, topic.ID, clientID(), "fake "+chattest.CrisisWord)
	if err != nil || res.Crisis == nil || res.Crisis.EscalationID == nil {
		t.Fatalf("%+v %v", res, err)
	}
	if len(e.store.ActiveTimers(model.TimerCrisisEscalation)) != 1 {
		t.Fatal("retry timer cancelled although the escalation was not recorded")
	}
	e.store.FailOn("SetMessageEscalation", nil)
	if err := e.inTx(func(tx port.Tx) error { return e.svc.HandleCrisisEscalation(e.ctx, tx, res.Message.ID) }); err != nil {
		t.Fatal(err)
	}
	if e.crisis.Calls != 2 || len(e.crisis.Escalations) != 1 {
		t.Fatalf("calls %d, escalations %d", e.crisis.Calls, len(e.crisis.Escalations))
	}
}

func TestSendLimits(t *testing.T) {
	t.Run("rate limit across rooms", func(t *testing.T) {
		e := newEnv(t, service.Settings{RateLimit: 2, RateWindow: time.Minute})
		a, b := e.topicRoom("autism"), e.topicRoom("down_syndrome")
		author := e.member(true, nil)
		e.join(author, a.ID)
		e.join(author, b.ID)
		e.send(author, a.ID, fakeText)
		e.clock.Advance(time.Second)
		e.send(author, b.ID, fakeText)
		if _, err := e.svc.Send(e.ctx, author, a.ID, clientID(), fakeText); !errors.Is(err, model.ErrRateLimited) {
			t.Fatalf("third message: %v", err)
		}
		e.clock.Advance(time.Minute)
		e.send(author, a.ID, fakeText)
	})
	t.Run("slow mode per room", func(t *testing.T) {
		e := newEnv(t, service.Settings{})
		room, free := e.topicRoom("autism"), e.topicRoom("down_syndrome")
		author := e.member(true, nil)
		e.join(author, room.ID)
		e.join(author, free.ID)
		secs := 60
		if err := e.svc.ModerateRoom(e.ctx, staff("admin"), room.ID, model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: &secs}); err != nil {
			t.Fatal(err)
		}
		e.send(author, room.ID, fakeText)
		e.clock.Advance(30 * time.Second)
		if _, err := e.svc.Send(e.ctx, author, room.ID, clientID(), fakeText); !errors.Is(err, model.ErrSlowMode) {
			t.Fatalf("within slow mode: %v", err)
		}
		e.send(author, free.ID, fakeText)
		e.clock.Advance(30 * time.Second)
		e.send(author, room.ID, fakeText)
	})
	t.Run("mute for an hour", func(t *testing.T) {
		e := newEnv(t, service.Settings{})
		room := e.topicRoom("autism")
		author := e.member(true, nil)
		e.join(author, room.ID)
		if err := e.svc.ModerateRoom(e.ctx, staff("admin"), room.ID, model.Moderation{Action: model.ActionMute1h, MemberID: &author.MemberID}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Send(e.ctx, author, room.ID, clientID(), fakeText); !errors.Is(err, model.ErrBanned) {
			t.Fatalf("muted: %v", err)
		}
		e.clock.Advance(model.MuteDuration)
		e.send(author, room.ID, fakeText)
	})
	t.Run("store failures", func(t *testing.T) {
		for _, method := range []string{"GetRestriction", "CountAuthorMessagesSince", "LastAuthorMessageAt", "NextSeq", "Schedule", "Lock", "GetMessageByClientID"} {
			e := newEnv(t, service.Settings{})
			room := e.topicRoom("autism")
			secs := 10
			if err := e.svc.ModerateRoom(e.ctx, staff("admin"), room.ID, model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: &secs}); err != nil {
				t.Fatal(err)
			}
			author := e.member(true, nil)
			e.join(author, room.ID)
			e.store.FailOn(method, errors.New("database down"))
			if _, err := e.svc.Send(e.ctx, author, room.ID, clientID(), fakeText); err == nil {
				t.Fatalf("%s failure not returned", method)
			}
			if len(e.store.Messages(room.ID)) != 0 {
				t.Fatalf("%s failure left a message", method)
			}
		}
		e := newEnv(t, service.Settings{})
		room := e.topicRoom("autism")
		author := e.member(true, nil)
		e.join(author, room.ID)
		e.store.FailOn("Publish", errors.New("database down"))
		if _, err := e.svc.Send(e.ctx, author, room.ID, clientID(), "fake www.example.invalid"); err == nil {
			t.Fatal("held event failure not returned")
		}
		if r, _ := e.store.Room(room.ID); r.LastSeq != 0 {
			t.Fatal("failed send kept its sequence number")
		}
	})
}

func TestBlocksHideMessagesBothWays(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	a, b, c := e.member(true, nil), e.member(true, nil), e.member(true, nil)
	for _, p := range []auth.Principal{a, b, c} {
		e.join(p, room.ID)
	}
	ma := e.send(a, room.ID, fakeText).Message
	mb := e.send(b, room.ID, fakeText).Message
	mc := e.send(c, room.ID, fakeText).Message
	e.members.Block(a.MemberID, b.MemberID) // a blocked b

	cases := []struct {
		reader auth.Principal
		want   []uuid.UUID
	}{
		{a, []uuid.UUID{ma.ID, mc.ID}},
		{b, []uuid.UUID{mb.ID, mc.ID}}, // the blocked one does not see the blocker either
		{c, []uuid.UUID{ma.ID, mb.ID, mc.ID}},
	}
	for _, tc := range cases {
		got := ids(e.list(tc.reader, room.ID))
		if len(got) != len(tc.want) {
			t.Fatalf("reader %v sees %v, want %v", tc.reader.MemberID, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("reader %v sees %v, want %v", tc.reader.MemberID, got, tc.want)
			}
		}
	}

	// The gateway path filters the same way.
	o, err := e.svc.LoadOpened(e.ctx, mb.ID)
	if err != nil || o.PlainText != fakeText || o.AuthorName != fakeName {
		t.Fatalf("load opened: %+v %v", o, err)
	}
	if _, ok, err := e.svc.Render(e.ctx, a.MemberID, o); ok || err != nil {
		t.Fatalf("blocked author rendered: %v %v", ok, err)
	}
	if v, ok, err := e.svc.Render(e.ctx, c.MemberID, o); !ok || err != nil || v.Text == nil {
		t.Fatalf("render for a third member: %+v %v %v", v, ok, err)
	}

	// Block lookups fail closed.
	e.members.Err = errors.New("membership down")
	if _, err := e.svc.ListMessages(e.ctx, c, room.ID, nil, nil, 0); err == nil {
		t.Fatal("history rendered without block information")
	}
	if _, _, err := e.svc.Render(e.ctx, c.MemberID, o); err == nil {
		t.Fatal("rendered without block information")
	}
	e.svc.Blocks = nil
	e.members.Err = nil
	if _, _, err := e.svc.Render(e.ctx, c.MemberID, o); err == nil {
		t.Fatal("rendered without a blocks port")
	}
	// Her own message never needs a block lookup.
	if _, ok, err := e.svc.Render(e.ctx, b.MemberID, o); !ok || err != nil {
		t.Fatalf("own message: %v %v", ok, err)
	}
}

func TestListMessagesPagingAndVisibility(t *testing.T) {
	e := newEnv(t, service.Settings{RateLimit: 1000})
	room := e.topicRoom("autism")
	author, reader := e.member(true, nil), e.member(true, nil)
	e.join(author, room.ID)
	e.join(reader, room.ID)
	var visible []uuid.UUID
	for i := range 30 {
		text := fakeText
		if i%3 == 0 {
			text = "fake www.example.invalid" // held: invisible to the reader
		}
		m := e.send(author, room.ID, text).Message
		if i%3 != 0 {
			visible = append(visible, m.ID)
		}
	}

	page := func(after, before *int64, limit int) []uuid.UUID {
		t.Helper()
		vs, err := e.svc.ListMessages(e.ctx, reader, room.ID, after, before, limit)
		if err != nil {
			t.Fatal(err)
		}
		return ids(vs)
	}
	i64 := func(v int64) *int64 { return &v }

	if got := page(nil, nil, 0); len(got) != len(visible) {
		t.Fatalf("default page: %d, want %d", len(got), len(visible))
	}
	// A page is filled from further rows past held ones.
	first := page(nil, nil, 5)
	if len(first) != 5 || first[0] != visible[0] || first[4] != visible[4] {
		t.Fatalf("first page %v", first)
	}
	if got := page(i64(28), nil, 50); len(got) != 2 {
		t.Fatalf("after 28: %v", got)
	}
	back := page(nil, i64(31), 4)
	if len(back) != 4 || back[3] != visible[len(visible)-1] || back[0] != visible[len(visible)-4] {
		t.Fatalf("scroll back page %v", back)
	}
	if got := page(nil, i64(2), 10); len(got) != 0 {
		t.Fatalf("before the first visible message: %v", got)
	}

	invalid := []struct {
		name          string
		after, before *int64
		limit         int
	}{
		{"limit above max", nil, nil, model.PageMax + 1},
		{"negative after", i64(-1), nil, 0},
		{"before zero", nil, i64(0), 0},
		{"both cursors", i64(1), i64(5), 0},
	}
	for _, tc := range invalid {
		if _, err := e.svc.ListMessages(e.ctx, reader, room.ID, tc.after, tc.before, tc.limit); !errors.Is(err, model.ErrInvalidInput) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	outsider := e.member(true, nil)
	if _, err := e.svc.ListMessages(e.ctx, outsider, room.ID, nil, nil, 0); !errors.Is(err, model.ErrNotMember) {
		t.Fatalf("outsider reads history: %v", err)
	}
	if _, err := e.svc.ListMessages(e.ctx, staff("admin"), room.ID, nil, nil, 0); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("staff reads history: %v", err)
	}

	e.store.FailOn("ListMessagesBefore", errors.New("database down"))
	if _, err := e.svc.ListMessages(e.ctx, reader, room.ID, nil, i64(10), 0); err == nil {
		t.Fatal("want store error (before)")
	}
	e.store.FailOn("ListMessagesAfter", errors.New("database down"))
	if _, err := e.svc.ListMessages(e.ctx, reader, room.ID, nil, nil, 0); err == nil {
		t.Fatal("want store error (after)")
	}
}

func TestListMessagesBeforeFillsAcrossBatches(t *testing.T) {
	e := newEnv(t, service.Settings{RateLimit: 1000})
	room := e.topicRoom("autism")
	author, reader := e.member(true, nil), e.member(true, nil)
	e.join(author, room.ID)
	e.join(reader, room.ID)
	// 150 held messages after one visible: scrolling back must look past a
	// whole raw batch (100) to find it.
	want := e.send(author, room.ID, fakeText).Message.ID
	for range 150 {
		e.send(author, room.ID, "fake www.example.invalid")
	}
	before := int64(1000)
	vs, err := e.svc.ListMessages(e.ctx, reader, room.ID, nil, &before, 10)
	if err != nil || len(vs) != 1 || vs[0].ID != want {
		t.Fatalf("got %v %v", ids(vs), err)
	}
	after := int64(0)
	vs, err = e.svc.ListMessages(e.ctx, reader, room.ID, &after, nil, 1)
	if err != nil || len(vs) != 1 || vs[0].ID != want {
		t.Fatalf("after: %v %v", ids(vs), err)
	}
}

func TestDeletedAndHiddenMessagesRender(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	gone, hidden, reader := e.member(true, nil), e.member(true, nil), e.member(true, nil)
	for _, p := range []auth.Principal{gone, hidden, reader} {
		e.join(p, room.ID)
	}
	mg := e.send(gone, room.ID, fakeText).Message
	mh := e.send(hidden, room.ID, fakeText).Message
	if err := e.svc.ModerateRoom(e.ctx, staff("admin"), room.ID, model.Moderation{Action: model.ActionHideMessage, MessageID: &mh.ID}); err != nil {
		t.Fatal(err)
	}
	e.sealer.Shred(gone.MemberID)

	vs := e.list(reader, room.ID)
	if len(vs) != 2 {
		t.Fatalf("views %+v", vs)
	}
	if vs[0].ID != mg.ID || vs[0].Status != model.StatusDeleted || vs[0].Text != nil {
		t.Fatalf("shredded author: %+v", vs[0])
	}
	if vs[1].ID != mh.ID || vs[1].Status != model.StatusHidden || vs[1].Text != nil {
		t.Fatalf("hidden: %+v", vs[1])
	}
	o, err := e.svc.LoadOpened(e.ctx, mh.ID)
	if err != nil || o.PlainText != "" {
		t.Fatalf("hidden text opened for fan-out: %+v %v", o, err)
	}
	if _, err := e.svc.LoadOpened(e.ctx, uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown message: %v", err)
	}

	// A sealer failure other than a shredded key is an error, not "deleted".
	e.svc.Sealer = nil
	if _, err := e.svc.ListMessages(e.ctx, reader, room.ID, nil, nil, 0); err == nil {
		t.Fatal("history without a sealer")
	}
	if _, err := e.svc.LoadOpened(e.ctx, mg.ID); err == nil {
		t.Fatal("load without a sealer")
	}
	e.svc.Sealer = e.sealer
	e.members.Err = errors.New("membership down")
	if _, err := e.svc.LoadOpened(e.ctx, mg.ID); err == nil {
		t.Fatal("load without author names")
	}
}

func TestCatchup(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	author := e.member(true, nil)
	e.join(author, room.ID)
	for range 3 {
		e.send(author, room.ID, fakeText)
	}
	views, scanned, more, err := e.svc.Catchup(e.ctx, author, room.ID, 1, 0)
	if err != nil || len(views) != 2 || scanned != 3 || more {
		t.Fatalf("catchup: %d %d %v %v", len(views), scanned, more, err)
	}
	views, scanned, more, err = e.svc.Catchup(e.ctx, author, room.ID, 0, 2)
	if err != nil || len(views) != 2 || scanned != 2 || !more {
		t.Fatalf("limited: %d %d %v %v", len(views), scanned, more, err)
	}
	if _, scanned, _, err := e.svc.Catchup(e.ctx, staff("admin"), room.ID, 7, 0); !errors.Is(err, model.ErrUnauthorized) || scanned != 7 {
		t.Fatalf("staff: %d %v", scanned, err)
	}
	e.store.FailOn("ListMessagesAfter", errors.New("database down"))
	if _, scanned, _, err := e.svc.Catchup(e.ctx, author, room.ID, 1, 0); err == nil || scanned != 1 {
		t.Fatalf("failure must keep the cursor: %d %v", scanned, err)
	}
}
