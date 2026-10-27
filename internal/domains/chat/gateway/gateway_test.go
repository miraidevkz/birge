package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"birge/internal/domains/chat/adapter"
	"birge/internal/domains/chat/chattest"
	"birge/internal/domains/chat/gateway"
	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/testinfra"
)

const fakeText = "fake hello from the example playground"

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type stack struct {
	svc     *service.Service
	members *chattest.FakeMembers
	auth    *chattest.FakeAuth
	room    model.Room
}

func newStack(t *testing.T, fanout port.Fanout) *stack {
	t.Helper()
	pool := testinfra.Postgres(t)
	st := &stack{members: chattest.NewFakeMembers(), auth: &chattest.FakeAuth{}}
	st.svc = &service.Service{
		Store: &adapter.PostgresStore{Pool: pool}, Sealer: &chattest.FakeSealer{}, Members: st.members, Blocks: st.members, Mentors: st.members,
		Crisis: &chattest.FakeCrisis{}, Fanout: fanout, Clock: &chattest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		Logger: discard, Settings: service.Settings{NewcomerHeld: -1, RateLimit: 1000},
	}
	room, err := st.svc.EnsureTopicRoom(context.Background(), "autism", "chat.room.topic.autism")
	if err != nil {
		t.Fatal(err)
	}
	st.room = room
	return st
}

// member creates a joined member with a session token.
func (s *stack) member(t *testing.T) (auth.Principal, string) {
	t.Helper()
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	s.members.Add(p.MemberID, model.Profile{Verified: true, FirstName: "Fakename", Lang: "ru"})
	if _, err := s.svc.JoinRoom(context.Background(), p, s.room.ID); err != nil {
		t.Fatal(err)
	}
	token := "token-" + p.MemberID.String()
	s.auth.Grant(token, p)
	return p, token
}

func serve(t *testing.T, g *gateway.Gateway) string {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

type client struct {
	t  *testing.T
	ws *websocket.Conn
}

func dial(t *testing.T, url, token string) *client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	ws.SetReadLimit(1 << 20)
	t.Cleanup(func() { _ = ws.CloseNow() })
	c := &client{t: t, ws: ws}
	c.write(map[string]any{"type": "auth", "token": token})
	return c
}

func (c *client) write(v any) {
	c.t.Helper()
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) read() map[string]any {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.ws.Read(ctx)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		c.t.Fatal(err)
	}
	return m
}

// readType reads frames until one of type typ.
func (c *client) readType(typ string) map[string]any {
	c.t.Helper()
	for range 50 {
		if f := c.read(); f["type"] == typ {
			return f
		}
	}
	c.t.Fatalf("no %s frame", typ)
	return nil
}

func (c *client) subscribe(roomID uuid.UUID, after int64) {
	c.write(map[string]any{"type": "subscribe", "rooms": []map[string]any{{"roomId": roomID, "afterSeq": after}}})
}

func natsFanout(t *testing.T, url string) *adapter.NATSFanout {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return &adapter.NATSFanout{Conn: nc, Logger: discard}
}

func waitSubscribers(t *testing.T, g *gateway.Gateway, roomID uuid.UUID, n int) {
	t.Helper()
	for range 200 {
		if g.Subscribers(roomID) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscribers %d, want %d", g.Subscribers(roomID), n)
}

// TestHappyPathAcrossReplicas: two gateway replicas share only NATS and
// PostgreSQL; a message sent on one reaches a member connected to the other.
func TestHappyPathAcrossReplicas(t *testing.T) {
	natsURL := testinfra.NATS(t)
	st := newStack(t, natsFanout(t, natsURL))
	a, tokenA := st.member(t)
	_, tokenB := st.member(t)
	g1 := &gateway.Gateway{Chat: st.svc, Auth: st.auth, Bus: natsFanout(t, natsURL), Logger: discard}
	g2 := &gateway.Gateway{Chat: st.svc, Auth: st.auth, Bus: natsFanout(t, natsURL), Logger: discard}
	ca := dial(t, serve(t, g1), tokenA)
	cb := dial(t, serve(t, g2), tokenB)
	ca.subscribe(st.room.ID, 0)
	cb.subscribe(st.room.ID, 0)
	waitSubscribers(t, g1, st.room.ID, 1)
	waitSubscribers(t, g2, st.room.ID, 1)
	time.Sleep(50 * time.Millisecond) // NATS interest propagates

	clientID := uuid.Must(uuid.NewV7())
	ca.write(map[string]any{"type": "send", "roomId": st.room.ID, "clientId": clientID, "text": fakeText})
	ack := ca.readType("ack")
	if ack["clientId"] != clientID.String() || ack["status"] != model.StatusVisible || ack["seq"].(float64) != 1 {
		t.Fatalf("ack %v", ack)
	}
	got := cb.readType("message")
	if got["text"] != fakeText || got["authorId"] != a.MemberID.String() || got["seq"].(float64) != 1 || got["mine"] != false || got["status"] != model.StatusVisible {
		t.Fatalf("message %v", got)
	}
	if own := ca.readType("message"); own["mine"] != true {
		t.Fatalf("own message %v", own)
	}
	// A repeated clientId is acknowledged with the same message and not re-sent.
	ca.write(map[string]any{"type": "send", "roomId": st.room.ID, "clientId": clientID, "text": fakeText})
	if again := ca.readType("ack"); again["messageId"] != ack["messageId"] {
		t.Fatalf("replay ack %v", again)
	}

	// Crisis: private frame to the author, nothing to the other member.
	ca.write(map[string]any{"type": "send", "roomId": st.room.ID, "clientId": uuid.Must(uuid.NewV7()), "text": chattest.CrisisWord})
	crisis := ca.read()
	if crisis["type"] != "crisis" || len(crisis["crisisPhones"].([]any)) == 0 || crisis["escalationId"] == nil {
		t.Fatalf("crisis frame %v", crisis)
	}
	if held := ca.readType("ack"); held["status"] != model.StatusHeld {
		t.Fatalf("crisis ack %v", held)
	}
	ca.write(map[string]any{"type": "send", "roomId": st.room.ID, "clientId": uuid.Must(uuid.NewV7()), "text": "fake second"})
	if next := cb.readType("message"); next["text"] != "fake second" || next["seq"].(float64) != 3 {
		t.Fatalf("other member got %v (crisis message leaked?)", next)
	}

	cb.write(map[string]any{"type": "ping"})
	cb.readType("pong")
	cb.write(map[string]any{"type": "send", "roomId": uuid.New(), "clientId": uuid.Must(uuid.NewV7()), "text": fakeText})
	if e := cb.readType("error"); e["code"] != "not_member" {
		t.Fatalf("error %v", e)
	}
	cb.write(map[string]any{"type": "send", "roomId": st.room.ID, "clientId": uuid.New(), "text": fakeText})
	if e := cb.readType("error"); e["code"] != "invalid" {
		t.Fatalf("error %v", e)
	}
}

func TestResumeBySeqAndGapFill(t *testing.T) {
	bus := &chattest.Bus{}
	st := newStack(t, bus)
	a, _ := st.member(t)
	_, tokenB := st.member(t)
	g := &gateway.Gateway{Chat: st.svc, Auth: st.auth, Bus: bus, Logger: discard}
	url := serve(t, g)
	ctx := context.Background()
	send := func(text string) model.SendResult {
		res, err := st.svc.Send(ctx, a, st.room.ID, uuid.Must(uuid.NewV7()), text)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	send("fake one")
	send("fake two")
	// Resume after seq 1: only seq 2 and later, then live.
	cb := dial(t, url, tokenB)
	cb.subscribe(st.room.ID, 1)
	if m := cb.readType("message"); m["seq"].(float64) != 2 || m["text"] != "fake two" {
		t.Fatalf("resume %v", m)
	}
	waitSubscribers(t, g, st.room.ID, 1)
	// A lost notice is repaired when the next one shows a gap.
	bus.Drop = true
	send("fake three")
	bus.Drop = false
	send("fake four")
	if m := cb.readType("message"); m["seq"].(float64) != 3 || m["text"] != "fake three" {
		t.Fatalf("gap fill %v", m)
	}
	if m := cb.readType("message"); m["seq"].(float64) != 4 {
		t.Fatalf("after gap %v", m)
	}
	// Disconnect, more messages, reconnect with the last seq.
	_ = cb.ws.Close(websocket.StatusNormalClosure, "")
	send("fake five")
	send("fake six")
	cb2 := dial(t, url, tokenB)
	cb2.subscribe(st.room.ID, 4)
	if m := cb2.readType("message"); m["seq"].(float64) != 5 {
		t.Fatalf("reconnect %v", m)
	}
	if m := cb2.readType("message"); m["seq"].(float64) != 6 {
		t.Fatalf("reconnect %v", m)
	}
	// Leaving the room ends the subscription.
	pb := auth.Principal{MemberID: uuid.MustParse(strings.TrimPrefix(tokenB, "token-"))}
	if err := st.svc.LeaveRoom(ctx, pb, st.room.ID); err != nil {
		t.Fatal(err)
	}
	for range 200 {
		if g.Subscribers(st.room.ID) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := g.Subscribers(st.room.ID); n != 0 {
		t.Fatalf("left member still subscribed (%d)", n)
	}
}

func TestInvalidAuthCloses4401(t *testing.T) {
	bus := &chattest.Bus{}
	st := newStack(t, bus)
	g := &gateway.Gateway{Chat: st.svc, Auth: st.auth, Bus: bus, Logger: discard}
	c := dial(t, serve(t, g), "wrong-token")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.ws.Read(ctx)
	if websocket.CloseStatus(err) != gateway.CloseUnauthorized {
		t.Fatalf("close status %v (%v)", websocket.CloseStatus(err), err)
	}
}

// fakeChat serves the gateway without a database; Render blocks for the
// slow reader until released.
type fakeChat struct {
	room    uuid.UUID
	slow    uuid.UUID
	release chan struct{}
	once    sync.Once
	seqs    sync.Map // message id -> seq
}

func (f *fakeChat) Send(context.Context, auth.Principal, uuid.UUID, uuid.UUID, string) (model.SendResult, error) {
	return model.SendResult{}, errors.New("not used")
}

func (f *fakeChat) CanSubscribe(context.Context, auth.Principal, uuid.UUID) error { return nil }

func (f *fakeChat) Catchup(_ context.Context, _ auth.Principal, _ uuid.UUID, after int64, _ int) ([]model.MessageView, int64, bool, error) {
	return nil, after, false, nil
}

func (f *fakeChat) LoadOpened(_ context.Context, id uuid.UUID) (model.Opened, error) {
	seq, _ := f.seqs.Load(id)
	s, _ := seq.(int64)
	return model.Opened{Message: model.Message{ID: id, RoomID: f.room, Seq: s, Status: model.StatusVisible, AuthorID: uuid.New()}, PlainText: "fake"}, nil
}

func (f *fakeChat) Render(ctx context.Context, reader uuid.UUID, o model.Opened) (model.MessageView, bool, error) {
	if reader == f.slow {
		select {
		case <-f.release:
		case <-ctx.Done():
		}
	}
	v, ok := model.View(o, reader)
	return v, ok, nil
}

func (f *fakeChat) TouchPresence(context.Context, uuid.UUID, []uuid.UUID, bool) error { return nil }

func (f *fakeChat) PresenceInterval() time.Duration { return time.Hour }

func TestSlowClientDisconnected(t *testing.T) {
	room := uuid.New()
	slow, fast := auth.Principal{MemberID: uuid.New()}, auth.Principal{MemberID: uuid.New()}
	chat := &fakeChat{room: room, slow: slow.MemberID, release: make(chan struct{})}
	t.Cleanup(func() { chat.once.Do(func() { close(chat.release) }) })
	authn := &chattest.FakeAuth{}
	authn.Grant("slow", slow)
	authn.Grant("fast", fast)
	bus := &chattest.Bus{}
	g := &gateway.Gateway{Chat: chat, Auth: authn, Bus: bus, Logger: discard, Options: gateway.Options{QueueSize: 4}}
	url := serve(t, g)
	cs := dial(t, url, "slow")
	cf := dial(t, url, "fast")
	cs.subscribe(room, 0)
	cf.subscribe(room, 0)
	waitSubscribers(t, g, room, 2)
	time.Sleep(50 * time.Millisecond) // both subscriptions processed by their writers

	const n = 50
	for i := 1; i <= n; i++ {
		id := uuid.New()
		chat.seqs.Store(id, int64(i))
		published := make(chan struct{})
		go func() {
			defer close(published)
			_ = bus.Publish(context.Background(), model.Notice{Kind: model.NoticeNew, RoomID: room, MessageID: &id, Seq: int64(i)})
		}()
		select {
		case <-published: // the fan-out never blocks on the slow client
		case <-time.After(5 * time.Second):
			t.Fatal("fan-out blocked by a slow client")
		}
		if m := cf.readType("message"); m["seq"].(float64) != float64(i) {
			t.Fatalf("fast client got seq %v, want %d", m["seq"], i)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, _, err := cs.ws.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != gateway.CloseSlowConsumer {
				t.Fatalf("slow client close status %v (%v)", websocket.CloseStatus(err), err)
			}
			break
		}
	}
	chat.once.Do(func() { close(chat.release) })
}
