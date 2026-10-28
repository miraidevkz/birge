package adapter_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/chat/adapter"
	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/outbox"
	"birge/internal/platform/testinfra"
)

func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// TestEraseMemberPostgres: account deletion turns her messages into
// tombstones (legal holds kept), removes her memberships, reports,
// restrictions and mentor assignments, cancels pending pushes, keeps the
// other member's conversation and is idempotent.
func TestEraseMemberPostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	gone, other := e.member(true, nil), e.member(true, nil)
	e.join(gone, room.ID)
	e.join(other, room.ID)
	e.members.SetMentor(gone.MemberID, true)
	admin := staff("admin")
	if err := e.svc.AssignMentor(e.ctx, admin, room.ID, gone.MemberID, true); err != nil {
		t.Fatal(err)
	}
	direct, err := e.svc.OpenDirectRoom(e.ctx, uuid.New(), model.OriginMatch, uuid.New(), gone.MemberID, other.MemberID)
	if err != nil {
		t.Fatal(err)
	}
	m1 := e.send(gone, room.ID, fakeText).Message
	m2 := e.send(gone, direct.ID, fakeText).Message
	kept := e.send(gone, room.ID, fakeText).Message
	theirs := e.send(other, room.ID, fakeText).Message
	if err := e.svc.SetLegalHold(e.ctx, admin, kept.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ReportMessage(e.ctx, gone, theirs.ID, "spam"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ReportMessage(e.ctx, other, m1.ID, "harassment"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ModerateRoom(e.ctx, admin, room.ID, model.Moderation{Action: model.ActionMute1h, MemberID: &gone.MemberID}); err != nil {
		t.Fatal(err)
	}

	st := &adapter.PostgresStore{Pool: e.pool}
	now := e.clock.Now()
	for run := range 2 {
		held, err := st.EraseMember(e.ctx, gone.MemberID, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(held) != 1 || held[0] != kept.ID {
			t.Fatalf("run %d: held %v, want only the legal-hold message", run, held)
		}
	}

	if n := e.count(`SELECT count(*) FROM chat.messages WHERE author_id = $1 AND body IS NULL AND purged_at IS NOT NULL`, gone.MemberID); n != 2 {
		t.Fatalf("purged messages %d, want 2", n)
	}
	if n := e.count(`SELECT count(*) FROM chat.messages WHERE id = $1 AND body IS NOT NULL AND purged_at IS NULL`, kept.ID); n != 1 {
		t.Fatal("legal-hold message purged")
	}
	if n := e.count(`SELECT count(*) FROM chat.messages WHERE id = $1 AND body IS NOT NULL`, theirs.ID); n != 1 {
		t.Fatal("the other member's message was touched")
	}
	for table, want := range map[string]int{
		"chat.room_members": 0, "chat.room_restrictions": 0, "chat.room_mentors": 0,
	} {
		if n := e.count(`SELECT count(*) FROM `+table+` WHERE member_id = $1`, gone.MemberID); n != want {
			t.Fatalf("%s rows %d, want %d", table, n, want)
		}
	}
	if n := e.count(`SELECT count(*) FROM chat.reports WHERE reporter_id = $1`, gone.MemberID); n != 0 {
		t.Fatalf("her reports kept: %d", n)
	}
	// A report about her message is the other member's record and stays.
	if n := e.count(`SELECT count(*) FROM chat.reports WHERE message_id = $1`, m1.ID); n != 1 {
		t.Fatalf("report on her message: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM platform.timers WHERE kind = $1 AND subject_id = ANY($2) AND done_at IS NULL`,
		model.TimerPush, []uuid.UUID{m1.ID, m2.ID}); n != 0 {
		t.Fatalf("pending pushes for erased messages: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM platform.audit_log WHERE action = 'chat.member_deleted' AND object_id = $1`, gone.MemberID); n != 1 {
		t.Fatalf("member_deleted audit rows %d, want exactly 1", n)
	}

	// The other member keeps both conversations; her messages read as deleted.
	for _, r := range []uuid.UUID{room.ID, direct.ID} {
		for _, v := range e.list(other, r) {
			if v.AuthorID == gone.MemberID && v.ID != kept.ID && (v.Status != model.StatusDeleted || v.Text != nil) {
				t.Fatalf("erased message rendered: %+v", v)
			}
		}
	}
	if len(e.list(other, direct.ID)) != 1 {
		t.Fatal("direct room history lost")
	}
}

func TestListMessagesBeforePostgres(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	p := e.member(true, nil)
	e.join(p, room.ID)
	for range 5 {
		e.send(p, room.ID, fakeText)
	}
	before := int64(4)
	vs, err := e.svc.ListMessages(e.ctx, p, room.ID, nil, &before, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].Seq != 2 || vs[1].Seq != 3 {
		t.Fatalf("scroll back: %+v", vs)
	}
}

func TestNATSFanoutRoundTrip(t *testing.T) {
	nc, err := nats.Connect(testinfra.NATS(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	f := &adapter.NATSFanout{Conn: nc}
	room, other := uuid.New(), uuid.New()
	got := make(chan model.Notice, 8)
	unsub, err := f.Subscribe(room, func(n model.Notice) { got <- n })
	if err != nil {
		t.Fatal(err)
	}
	msg := uuid.New()
	ctx := context.Background()
	// Noise first: another room, a malformed payload and a notice that names
	// another room on this room's subject are never delivered.
	if err := f.Publish(ctx, model.Notice{Kind: model.NoticeNew, RoomID: other, MessageID: &msg, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Publish(model.RoomSubject(room), []byte("not json")); err != nil {
		t.Fatal(err)
	}
	forged, _ := json.Marshal(model.Notice{Kind: model.NoticeNew, RoomID: other})
	if err := nc.Publish(model.RoomSubject(room), forged); err != nil {
		t.Fatal(err)
	}
	if err := f.Publish(ctx, model.Notice{Kind: model.NoticeNew, RoomID: room, MessageID: &msg, Seq: 7}); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-got:
		if n.RoomID != room || n.Seq != 7 || *n.MessageID != msg {
			t.Fatalf("notice %+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notice not delivered")
	}
	unsub()
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Publish(ctx, model.Notice{Kind: model.NoticeChanged, RoomID: room, MessageID: &msg, Seq: 7}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-got:
		t.Fatalf("delivered after unsubscribe or noise: %+v", n)
	case <-time.After(200 * time.Millisecond):
	}

	nc.Close()
	if err := f.Publish(ctx, model.Notice{Kind: model.NoticeNew, RoomID: room}); err == nil {
		t.Fatal("publish on a closed connection succeeded")
	}
	if _, err := f.Subscribe(room, func(model.Notice) {}); err == nil {
		t.Fatal("subscribe on a closed connection succeeded")
	}
}

func TestModerationConsumerJetStream(t *testing.T) {
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
	room := e.topicRoom("autism")
	author, reader := e.member(true, nil), e.member(true, nil)
	e.join(author, room.ID)
	e.join(reader, room.ID)
	held := e.send(author, room.ID, "fake see www.example.invalid").Message

	cons := &adapter.ModerationConsumer{JS: js, Service: e.svc}
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	eventID, item := uuid.New(), uuid.New()
	data, _ := json.Marshal(model.ModerationDecidedData{ItemID: item, Kind: model.ModerationKindChatMessage, ObjectID: held.ID, Approve: true})
	envBytes, _ := json.Marshal(outbox.Envelope{ID: eventID, Type: model.EventModerationDecided, Version: 1, OccurredAt: e.clock.Now(), Data: data})
	for range 2 { // redelivered under another broker id: processed_events dedups
		if _, err := js.Publish(e.ctx, outbox.Subject(model.EventModerationDecided), envBytes, jetstream.WithMsgID(uuid.NewString())); err != nil {
			t.Fatal(err)
		}
	}
	var visible bool
	for range 200 {
		if len(e.list(reader, room.ID)) == 1 {
			visible = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !visible {
		t.Fatal("approved message not visible to the reader")
	}
	settled := func() bool {
		c, err := js.Consumer(e.ctx, "EVENTS", "chat-moderation-decided")
		if err != nil {
			return false
		}
		ci, err := c.Info(e.ctx)
		return err == nil && ci.NumAckPending == 0 && ci.NumPending == 0
	}
	for range 200 {
		if settled() {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if n := e.count(`SELECT count(*) FROM platform.audit_log WHERE action = $1 AND actor_id = $2 AND actor_role = 'system'`, model.AuditRestore, item); n != 1 {
		t.Fatalf("restore audits %d, want 1", n)
	}

	// Other event types are acknowledged and ignored; malformed ones fail
	// (and are redelivered).
	other, _ := json.Marshal(outbox.Envelope{ID: uuid.New(), Type: "moderation.Other", Version: 1, Data: json.RawMessage(`{}`)})
	if err := cons.HandleMessage(e.ctx, other); err != nil {
		t.Fatalf("other type: %v", err)
	}
	if err := cons.HandleMessage(e.ctx, []byte("{")); err == nil {
		t.Fatal("malformed envelope accepted")
	}
	bad, _ := json.Marshal(outbox.Envelope{ID: uuid.New(), Type: model.EventModerationDecided, Version: 1, Data: json.RawMessage(`[1]`)})
	if err := cons.HandleMessage(e.ctx, bad); err == nil {
		t.Fatal("malformed payload accepted")
	}
}
