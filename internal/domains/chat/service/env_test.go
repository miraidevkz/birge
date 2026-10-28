package service_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/chattest"
	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/auth"
)

// Clearly fake data: no real names or texts.
const (
	fakeText = "fake hello from the example playground"
	fakeName = "Fakename"
)

// env is a chat service over the in-memory store and the chattest fakes.
type env struct {
	t       *testing.T
	ctx     context.Context
	store   *chattest.MemStore
	svc     *service.Service
	members *chattest.FakeMembers
	crisis  *chattest.FakeCrisis
	sealer  *chattest.FakeSealer
	bus     *chattest.Bus
	susp    *chattest.FakeSuspender
	clock   *chattest.Clock
	logs    *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// newEnv builds the service. The newcomer hold is off unless settings ask
// for it, so tests about other rules see visible messages.
func newEnv(t *testing.T, settings service.Settings) *env {
	t.Helper()
	e := &env{
		t: t, ctx: context.Background(), store: chattest.NewMemStore(),
		members: chattest.NewFakeMembers(), crisis: &chattest.FakeCrisis{}, sealer: &chattest.FakeSealer{},
		bus: &chattest.Bus{}, susp: &chattest.FakeSuspender{},
		clock: &chattest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		logs:  &syncBuffer{},
	}
	if settings.NewcomerHeld == 0 {
		settings.NewcomerHeld = -1
	}
	e.svc = &service.Service{
		Store: e.store, Sealer: e.sealer, Members: e.members, Blocks: e.members, Mentors: e.members,
		Suspenders: e.susp, Crisis: e.crisis, Fanout: e.bus, Clock: e.clock,
		Logger: slog.New(slog.NewJSONHandler(e.logs, nil)), Settings: settings,
	}
	t.Cleanup(e.checkNoText)
	return e
}

// checkNoText asserts that no message text reached the logs, an outbox
// event or a stored body in plaintext.
func (e *env) checkNoText() {
	logs := e.logs.String()
	if strings.Contains(logs, "fake hello") || strings.Contains(logs, chattest.CrisisWord) || strings.Contains(logs, fakeName) {
		e.t.Error("message text or a name in logs")
	}
	for _, ev := range e.store.Events() {
		if strings.Contains(fmt.Sprintf("%+v", ev.Data), "fake hello") {
			e.t.Error("message text in an outbox event")
		}
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

func (e *env) districtRoom(district uuid.UUID) model.Room {
	e.t.Helper()
	r, err := e.svc.EnsureDistrictRoom(e.ctx, district, "chat.room.district.fake")
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) directRoom(origin string, a, b auth.Principal) model.Room {
	e.t.Helper()
	r, err := e.svc.OpenDirectRoom(e.ctx, uuid.New(), origin, uuid.New(), a.MemberID, b.MemberID)
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

func clientID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func (e *env) send(p auth.Principal, roomID uuid.UUID, text string) model.SendResult {
	e.t.Helper()
	res, err := e.svc.Send(e.ctx, p, roomID, clientID(), text)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) list(p auth.Principal, roomID uuid.UUID) []model.MessageView {
	e.t.Helper()
	vs, err := e.svc.ListMessages(e.ctx, p, roomID, nil, nil, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return vs
}

// inTx runs a timer handler the way the timer worker does: inside one
// store transaction.
func (e *env) inTx(fn func(tx port.Tx) error) error {
	return e.store.InTx(e.ctx, fn)
}

func ids(vs []model.MessageView) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}
