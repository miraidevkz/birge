package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"birge/internal/domains/chat/chattest"
	"birge/internal/domains/chat/gateway"
	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/auth"
)

// memStack is a gateway over the chat service on the in-memory store: the
// frame protocol and its error codes without PostgreSQL or NATS.
type memStack struct {
	svc     *service.Service
	store   *chattest.MemStore
	members *chattest.FakeMembers
	auth    *chattest.FakeAuth
	room    model.Room
	url     string
}

func newMemStack(t *testing.T, opts gateway.Options) *memStack {
	t.Helper()
	st := &memStack{store: chattest.NewMemStore(), members: chattest.NewFakeMembers(), auth: &chattest.FakeAuth{}}
	bus := &chattest.Bus{}
	st.svc = &service.Service{
		Store: st.store, Sealer: &chattest.FakeSealer{}, Members: st.members, Blocks: st.members, Mentors: st.members,
		Crisis: &chattest.FakeCrisis{}, Fanout: bus, Clock: &chattest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		Logger: discard, Settings: service.Settings{NewcomerHeld: -1, RateLimit: 2},
	}
	room, err := st.svc.EnsureTopicRoom(context.Background(), "autism", "chat.room.topic.autism")
	if err != nil {
		t.Fatal(err)
	}
	st.room = room
	st.url = serve(t, &gateway.Gateway{Chat: st.svc, Auth: st.auth, Bus: bus, Logger: discard, Options: opts})
	return st
}

// member registers a verified member with a token; joined puts her in the room.
func (s *memStack) member(t *testing.T, joined bool) (auth.Principal, string) {
	t.Helper()
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	s.members.Add(p.MemberID, model.Profile{Verified: true, FirstName: "Fakename"})
	if joined {
		if _, err := s.svc.JoinRoom(context.Background(), p, s.room.ID); err != nil {
			t.Fatal(err)
		}
	}
	token := "token-" + p.MemberID.String()
	s.auth.Grant(token, p)
	return p, token
}

func closeStatus(t *testing.T, c *client) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := c.ws.Read(ctx); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func TestAuthFrameRules(t *testing.T) {
	st := newMemStack(t, gateway.Options{AuthTimeout: 200 * time.Millisecond})
	st.auth.Grant("staff-token", auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"admin"}})
	st.auth.Grant("no-member", auth.Principal{IdentityID: uuid.New()})
	_, good := st.member(t, true)

	rawDial := func(t *testing.T) *client {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ws, resp, err := websocket.Dial(ctx, st.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		t.Cleanup(func() { _ = ws.CloseNow() })
		return &client{t: t, ws: ws}
	}
	cases := []struct {
		name  string
		first func(c *client)
	}{
		{"staff session", func(c *client) { c.write(map[string]any{"type": "auth", "token": "staff-token"}) }},
		{"principal without member id", func(c *client) { c.write(map[string]any{"type": "auth", "token": "no-member"}) }},
		{"empty token", func(c *client) { c.write(map[string]any{"type": "auth", "token": ""}) }},
		{"first frame is not auth", func(c *client) { c.write(map[string]any{"type": "ping", "token": good}) }},
		{"not json", func(c *client) {
			if err := c.ws.Write(context.Background(), websocket.MessageText, []byte("{")); err != nil {
				t.Fatal(err)
			}
		}},
		{"binary frame", func(c *client) {
			if err := c.ws.Write(context.Background(), websocket.MessageBinary, []byte(`{"type":"auth"}`)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := rawDial(t)
			tc.first(c)
			if got := closeStatus(t, c); got != gateway.CloseUnauthorized {
				t.Fatalf("close status %v, want 4401", got)
			}
		})
	}

	// No auth frame within the timeout: the server ends the connection.
	// (coder/websocket drops the connection when the read deadline passes,
	// so the client sees no 4401 close frame here; see the package doc.)
	silent := rawDial(t)
	start := time.Now()
	_ = closeStatus(t, silent)
	if time.Since(start) > 3*time.Second {
		t.Fatal("silent connection kept open past the auth timeout")
	}

	// The good token is accepted: a ping is answered.
	c := dial(t, st.url, good)
	c.write(map[string]any{"type": "ping"})
	if f := c.read(); f["type"] != "pong" {
		t.Fatalf("frame %v", f)
	}
}

func TestFrameErrors(t *testing.T) {
	st := newMemStack(t, gateway.Options{})
	ctx := context.Background()
	mother, token := st.member(t, true)
	_, outsiderToken := st.member(t, false)
	c := dial(t, st.url, token)

	expectError := func(c *client, code string) {
		t.Helper()
		if f := c.read(); f["type"] != "error" || f["code"] != code {
			t.Fatalf("frame %v, want error %s", f, code)
		}
	}
	if err := c.ws.Write(ctx, websocket.MessageBinary, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	expectError(c, "invalid")
	if err := c.ws.Write(ctx, websocket.MessageText, []byte(`{"type":`)); err != nil {
		t.Fatal(err)
	}
	expectError(c, "invalid")

	c.write(map[string]any{"type": "subscribe", "rooms": []any{}})
	expectError(c, "invalid")
	tooMany := make([]map[string]any, model.MaxSubscribeRooms+1)
	for i := range tooMany {
		tooMany[i] = map[string]any{"roomId": uuid.New(), "afterSeq": 0}
	}
	c.write(map[string]any{"type": "subscribe", "rooms": tooMany})
	expectError(c, "invalid")
	c.write(map[string]any{"type": "subscribe", "rooms": []map[string]any{{"roomId": uuid.Nil, "afterSeq": 0}}})
	expectError(c, "invalid")
	c.write(map[string]any{"type": "subscribe", "rooms": []map[string]any{{"roomId": st.room.ID, "afterSeq": -1}}})
	expectError(c, "invalid")
	c.write(map[string]any{"type": "subscribe", "rooms": []map[string]any{{"roomId": uuid.New(), "afterSeq": 0}}})
	expectError(c, "not_member")

	send := func(c *client, room uuid.UUID, text string) {
		c.write(map[string]any{"type": "send", "roomId": room, "clientId": uuid.Must(uuid.NewV7()), "text": text})
	}
	c.write(map[string]any{"type": "send", "clientId": uuid.Must(uuid.NewV7()), "text": "fake hello"})
	expectError(c, "invalid")
	send(c, st.room.ID, "   ")
	expectError(c, "invalid")

	// The crisis frame reaches the author before the ack.
	send(c, st.room.ID, "fake "+chattest.CrisisWord)
	if f := c.read(); f["type"] != "crisis" || f["escalationId"] == nil || len(f["crisisPhones"].([]any)) == 0 {
		t.Fatalf("frame %v, want crisis", f)
	}
	if f := c.read(); f["type"] != "ack" || f["status"] != model.StatusHeld {
		t.Fatalf("frame %v, want ack held", f)
	}

	// Rate limit (2 per minute in this stack; the crisis message counted).
	send(c, st.room.ID, "fake hello")
	if f := c.read(); f["type"] != "ack" || f["status"] != model.StatusVisible {
		t.Fatalf("frame %v, want ack", f)
	}
	send(c, st.room.ID, "fake hello")
	expectError(c, "rate_limited")

	// Muted by staff: banned.
	if err := st.svc.ModerateRoom(ctx, auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"admin"}}, st.room.ID,
		model.Moderation{Action: model.ActionMute1h, MemberID: &mother.MemberID}); err != nil {
		t.Fatal(err)
	}
	other := dial(t, st.url, outsiderToken)
	send(other, st.room.ID, "fake hello")
	expectError(other, "not_member")

	// Slow mode for a fresh member.
	slowRoom, err := st.svc.EnsureTopicRoom(ctx, "down_syndrome", "chat.room.topic.down")
	if err != nil {
		t.Fatal(err)
	}
	p, tok := st.member(t, false)
	if _, err := st.svc.JoinRoom(ctx, p, slowRoom.ID); err != nil {
		t.Fatal(err)
	}
	secs := 60
	if err := st.svc.ModerateRoom(ctx, auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"admin"}}, slowRoom.ID,
		model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: &secs}); err != nil {
		t.Fatal(err)
	}
	s := dial(t, st.url, tok)
	send(s, slowRoom.ID, "fake hello")
	if f := s.read(); f["type"] != "ack" {
		t.Fatalf("frame %v", f)
	}
	send(s, slowRoom.ID, "fake hello")
	expectError(s, "slow_mode")
	if _, err := st.svc.JoinRoom(ctx, p, st.room.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.svc.ModerateRoom(ctx, auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"admin"}}, st.room.ID,
		model.Moderation{Action: model.ActionMute1h, MemberID: &p.MemberID}); err != nil {
		t.Fatal(err)
	}
	send(s, st.room.ID, "fake hello")
	expectError(s, "banned")

	// A database failure closes the connection (the client retries the
	// same clientId later); it is not reported as a frame.
	st.store.FailOn("InTx", errors.New("database down"))
	send(s, slowRoom.ID, "fake hello")
	if got := closeStatus(t, s); got != websocket.StatusInternalError {
		t.Fatalf("close status %v, want 1011", got)
	}
	o2 := dial(t, st.url, outsiderToken)
	o2.subscribe(st.room.ID, 0)
	if got := closeStatus(t, o2); got != websocket.StatusInternalError {
		t.Fatalf("subscribe failure close status %v, want 1011", got)
	}
}
