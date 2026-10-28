package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/chattest"
	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/auth"
)

// httpEnv serves the chat API over the generated router with an in-memory
// service. The test sets who is calling; the real middleware (session or
// staff auth) is wired in internal/wiring.
type httpEnv struct {
	t       *testing.T
	h       http.Handler
	as      auth.Principal
	svc     *service.Service
	store   *chattest.MemStore
	members *chattest.FakeMembers
	clock   *chattest.Clock
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	e := &httpEnv{t: t, store: chattest.NewMemStore(), members: chattest.NewFakeMembers(),
		clock: &chattest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}}
	e.svc = &service.Service{
		Store: e.store, Sealer: &chattest.FakeSealer{}, Members: e.members, Blocks: e.members, Mentors: e.members,
		Crisis: &chattest.FakeCrisis{}, Fanout: &chattest.Bus{}, Clock: e.clock,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Settings: service.Settings{NewcomerHeld: -1, RateLimit: 3},
	}
	who := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), e.as)))
		})
	}
	e.h = HandlerWithOptions(NewStrictHandler(&Server{Service: e.svc}, nil), StdHTTPServerOptions{Middlewares: []MiddlewareFunc{who}})
	return e
}

func (e *httpEnv) member(verified bool) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Add(p.MemberID, model.Profile{Verified: verified, FirstName: "Fakename"})
	return p
}

// do sends one request as p and returns the status and the decoded body.
func (e *httpEnv) do(p auth.Principal, method, path string, body any) (int, map[string]any, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	e.as = p
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var obj map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &obj) // arrays and empty bodies stay nil
	return rec.Code, obj, rec.Body.Bytes()
}

func staffP(roles ...string) auth.Principal {
	return auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: roles}
}

func TestHandlersMapEveryProblem(t *testing.T) {
	e := newHTTPEnv(t)
	ctx := context.Background()
	room, err := e.svc.EnsureTopicRoom(ctx, "autism", "chat.room.topic.autism")
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.svc.EnsureTopicRoom(ctx, "down_syndrome", "chat.room.topic.down")
	if err != nil {
		t.Fatal(err)
	}
	author, reader, outsider, unverified := e.member(true), e.member(true), e.member(true), e.member(false)
	muted, banned, slow, mentorCandidate := e.member(true), e.member(true), e.member(true), e.member(true)
	e.members.SetMentor(mentorCandidate.MemberID, true)
	for _, p := range []auth.Principal{author, reader, muted, banned} {
		if _, err := e.svc.JoinRoom(ctx, p, room.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.JoinRoom(ctx, slow, other.ID); err != nil {
		t.Fatal(err)
	}
	admin, support := staffP("admin"), staffP("support")
	msg, err := e.svc.Send(ctx, author, room.ID, uuid.Must(uuid.NewV7()), "fake hello")
	if err != nil {
		t.Fatal(err)
	}
	crisis, err := e.svc.Send(ctx, author, other.ID, uuid.Must(uuid.NewV7()), "fake "+chattest.CrisisWord)
	if err == nil || !errors.Is(err, model.ErrNotMember) || crisis.Crisis == nil {
		t.Fatalf("setup crisis outside the room: %v", err)
	}
	if _, err := e.svc.JoinRoom(ctx, author, other.ID); err != nil {
		t.Fatal(err)
	}
	crisis, err = e.svc.Send(ctx, author, other.ID, uuid.Must(uuid.NewV7()), "fake "+chattest.CrisisWord)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []model.Moderation{
		{Action: model.ActionMute1h, MemberID: &muted.MemberID},
		{Action: model.ActionBan, MemberID: &banned.MemberID},
	} {
		if err := e.svc.ModerateRoom(ctx, admin, room.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	slowSecs := 600
	if err := e.svc.ModerateRoom(ctx, admin, other.ID, model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: &slowSecs}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Send(ctx, slow, other.ID, uuid.Must(uuid.NewV7()), "fake hello"); err != nil {
		t.Fatal(err)
	}

	r := "/chat/rooms/" + room.ID.String()
	o := "/chat/rooms/" + other.ID.String()
	unknownRoom := "/chat/rooms/" + uuid.New().String()
	m := "/chat/messages/" + msg.Message.ID.String()
	hold := "/staff/chat/messages/" + msg.Message.ID.String() + "/legal-hold"
	mentors := "/staff/chat/rooms/" + room.ID.String() + "/mentors/"
	send := func(text string) map[string]any {
		return map[string]any{"clientId": uuid.Must(uuid.NewV7()), "text": text}
	}

	cases := []struct {
		name   string
		as     auth.Principal
		method string
		path   string
		body   any
		status int
		code   string
	}{
		{"list rooms", reader, http.MethodGet, "/chat/rooms", nil, 200, ""},
		{"list rooms bad kind", reader, http.MethodGet, "/chat/rooms?kind=secret", nil, 400, "invalid_request"},
		{"list rooms as staff", admin, http.MethodGet, "/chat/rooms", nil, 401, "unauthorized"},
		{"join", outsider, http.MethodPost, r + "/join", nil, 200, ""},
		{"join unverified", unverified, http.MethodPost, r + "/join", nil, 403, "forbidden"},
		{"join banned", banned, http.MethodPost, r + "/join", nil, 403, "banned"},
		{"join unknown", reader, http.MethodPost, unknownRoom + "/join", nil, 404, "not_found"},
		{"leave", outsider, http.MethodPost, r + "/leave", nil, 204, ""},
		{"leave unknown", reader, http.MethodPost, unknownRoom + "/leave", nil, 404, "not_found"},
		{"mute", reader, http.MethodPut, r + "/mute", map[string]any{"muted": true}, 204, ""},
		{"mute not member", outsider, http.MethodPut, r + "/mute", map[string]any{"muted": true}, 403, "not_member"},
		{"history", reader, http.MethodGet, r + "/messages?after=0&limit=10", nil, 200, ""},
		{"history before", reader, http.MethodGet, r + "/messages?before=5", nil, 200, ""},
		{"history limit zero", reader, http.MethodGet, r + "/messages?limit=0", nil, 400, "invalid_request"},
		{"history both cursors", reader, http.MethodGet, r + "/messages?after=1&before=3", nil, 400, "invalid_request"},
		{"history not member", outsider, http.MethodGet, r + "/messages", nil, 403, "not_member"},
		{"send", reader, http.MethodPost, r + "/messages", send("fake hello"), 201, ""},
		{"send blank", reader, http.MethodPost, r + "/messages", send("   "), 400, "invalid_request"},
		{"send not member", outsider, http.MethodPost, r + "/messages", send("fake hello"), 403, "not_member"},
		{"send muted", muted, http.MethodPost, r + "/messages", send("fake hello"), 403, "banned"},
		{"send slow mode", slow, http.MethodPost, o + "/messages", send("fake hello"), 429, "slow_mode"},
		{"send unknown room", reader, http.MethodPost, unknownRoom + "/messages", send("fake hello"), 404, "not_found"},
		{"report", reader, http.MethodPost, m + "/report", map[string]any{"reason": "spam"}, 204, ""},
		{"report bad reason", reader, http.MethodPost, m + "/report", map[string]any{"reason": "boring"}, 400, "invalid_request"},
		{"report own", author, http.MethodPost, m + "/report", map[string]any{"reason": "spam"}, 400, "invalid_request"},
		{"report unseen", outsider, http.MethodPost, m + "/report", map[string]any{"reason": "spam"}, 404, "not_found"},
		{"moderate", admin, http.MethodPost, r + "/moderation", map[string]any{"action": "hide_message", "messageId": msg.Message.ID}, 204, ""},
		{"moderate as member", reader, http.MethodPost, r + "/moderation", map[string]any{"action": "hide_message", "messageId": msg.Message.ID}, 403, "forbidden"},
		{"moderate bad action", admin, http.MethodPost, r + "/moderation", map[string]any{"action": "delete_room"}, 400, "invalid_request"},
		{"moderate missing message id", admin, http.MethodPost, r + "/moderation", map[string]any{"action": "hide_message"}, 400, "invalid_request"},
		{"moderate unknown room", admin, http.MethodPost, unknownRoom + "/moderation", map[string]any{"action": "hide_message", "messageId": msg.Message.ID}, 404, "not_found"},
		{"restore crisis", admin, http.MethodPost, o + "/moderation", map[string]any{"action": "restore_message", "messageId": crisis.Message.ID}, 409, "conflict"},
		{"moderate as member is unauthorized", auth.Principal{}, http.MethodPost, r + "/moderation", map[string]any{"action": "mute_1h", "memberId": reader.MemberID}, 401, "unauthorized"},
		{"assign mentor", admin, http.MethodPut, mentors + mentorCandidate.MemberID.String(), nil, 204, ""},
		{"assign mentor support", support, http.MethodPut, mentors + mentorCandidate.MemberID.String(), nil, 403, "forbidden"},
		{"assign mentor without role", admin, http.MethodPut, mentors + reader.MemberID.String(), nil, 409, "conflict"},
		{"assign mentor unknown room", admin, http.MethodPut, "/staff/chat/rooms/" + uuid.New().String() + "/mentors/" + mentorCandidate.MemberID.String(), nil, 404, "not_found"},
		{"assign mentor nil member", admin, http.MethodPut, mentors + uuid.Nil.String(), nil, 400, "invalid_request"},
		{"staff rooms", admin, http.MethodGet, "/staff/chat/rooms", nil, 200, ""},
		{"staff rooms support", support, http.MethodGet, "/staff/chat/rooms", nil, 403, "forbidden"},
		{"remove mentor", admin, http.MethodDelete, mentors + mentorCandidate.MemberID.String(), nil, 204, ""},
		{"remove mentor support", support, http.MethodDelete, mentors + mentorCandidate.MemberID.String(), nil, 403, "forbidden"},
		{"remove mentor unknown room", admin, http.MethodDelete, "/staff/chat/rooms/" + uuid.New().String() + "/mentors/" + mentorCandidate.MemberID.String(), nil, 404, "not_found"},
		{"legal hold", admin, http.MethodPut, hold, nil, 204, ""},
		{"legal hold support", support, http.MethodPut, hold, nil, 403, "forbidden"},
		{"legal hold unknown", admin, http.MethodPut, "/staff/chat/messages/" + uuid.New().String() + "/legal-hold", nil, 404, "not_found"},
		{"release hold", admin, http.MethodDelete, hold, nil, 204, ""},
		{"release hold support", support, http.MethodDelete, hold, nil, 403, "forbidden"},
		{"release hold unknown", admin, http.MethodDelete, "/staff/chat/messages/" + uuid.New().String() + "/legal-hold", nil, 404, "not_found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, body, raw := e.do(c.as, c.method, c.path, c.body)
			if status != c.status {
				t.Fatalf("status %d, want %d: %s", status, c.status, raw)
			}
			if c.code == "" {
				return
			}
			if body["code"] != c.code || body["status"] != float64(c.status) {
				t.Fatalf("problem %s, want code %s", raw, c.code)
			}
			if bytes.Contains(raw, []byte("fake hello")) {
				t.Fatal("message text echoed in a problem")
			}
		})
	}

	// The rate limit answers 429 rate_limited (3 per minute in this env;
	// "send" above used one).
	for range 2 {
		if st, _, raw := e.do(reader, http.MethodPost, r+"/messages", send("fake hello")); st != 201 {
			t.Fatalf("send: %d %s", st, raw)
		}
	}
	if st, body, raw := e.do(reader, http.MethodPost, r+"/messages", send("fake hello")); st != 429 || body["code"] != "rate_limited" {
		t.Fatalf("rate limit: %d %s", st, raw)
	}
}

func TestHandlerResponseBodies(t *testing.T) {
	e := newHTTPEnv(t)
	ctx := context.Background()
	room, err := e.svc.EnsureTopicRoom(ctx, "autism", "chat.room.topic.autism")
	if err != nil {
		t.Fatal(err)
	}
	author := e.member(true)
	if _, err := e.svc.JoinRoom(ctx, author, room.ID); err != nil {
		t.Fatal(err)
	}
	secs := 30
	if err := e.svc.ModerateRoom(ctx, staffP("admin"), room.ID, model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: &secs}); err != nil {
		t.Fatal(err)
	}

	st, body, raw := e.do(author, http.MethodPost, "/chat/rooms/"+room.ID.String()+"/messages",
		map[string]any{"clientId": uuid.Must(uuid.NewV7()), "text": "fake " + chattest.CrisisWord})
	if st != 201 || body["status"] != model.StatusHeld || body["escalationId"] == nil || body["mine"] != true {
		t.Fatalf("crisis send: %d %s", st, raw)
	}
	if phones, _ := body["crisisPhones"].([]any); len(phones) == 0 {
		t.Fatalf("crisis phones missing: %s", raw)
	}

	_, _, raw = e.do(author, http.MethodGet, "/chat/rooms?joined=true", nil)
	var rooms []map[string]any
	if err := json.Unmarshal(raw, &rooms); err != nil || len(rooms) != 1 {
		t.Fatalf("rooms %s %v", raw, err)
	}
	if rooms[0]["slowModeSeconds"] != float64(30) || rooms[0]["lastSeq"] != float64(1) || rooms[0]["joined"] != true {
		t.Fatalf("room %v", rooms[0])
	}

	mentor := e.member(true)
	e.members.Add(mentor.MemberID, model.Profile{Verified: true, FirstName: "Fakementor"})
	e.members.SetMentor(mentor.MemberID, true)
	if err := e.svc.AssignMentor(ctx, staffP("admin"), room.ID, mentor.MemberID, true); err != nil {
		t.Fatal(err)
	}
	_, body, raw = e.do(staffP("moderator"), http.MethodGet, "/staff/chat/rooms", nil)
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("staff rooms %s", raw)
	}
	ms, _ := items[0].(map[string]any)["mentors"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["displayName"] != "Fakementor" {
		t.Fatalf("mentors %s", raw)
	}
}

// Unexpected errors are not mapped to a problem: they reach the 500 handler.
func TestHandlersPassUnexpectedErrors(t *testing.T) {
	e := newHTTPEnv(t)
	member, admin := e.member(true), staffP("admin")
	room, msg := uuid.New().String(), uuid.New().String()
	// Assigning checks the mentor role before the store: give it.
	mentor := uuid.New()
	e.members.SetMentor(mentor, true)
	e.store.FailOn("InTx", errors.New("database down"))
	cases := []struct {
		as           auth.Principal
		method, path string
		body         any
	}{
		{member, http.MethodGet, "/chat/rooms", nil},
		{member, http.MethodPost, "/chat/rooms/" + room + "/join", nil},
		{member, http.MethodPost, "/chat/rooms/" + room + "/leave", nil},
		{member, http.MethodPut, "/chat/rooms/" + room + "/mute", map[string]any{"muted": false}},
		{member, http.MethodGet, "/chat/rooms/" + room + "/messages", nil},
		{member, http.MethodPost, "/chat/rooms/" + room + "/messages", map[string]any{"clientId": uuid.Must(uuid.NewV7()), "text": "fake hello"}},
		{member, http.MethodPost, "/chat/messages/" + msg + "/report", map[string]any{"reason": "spam"}},
		{admin, http.MethodPost, "/chat/rooms/" + room + "/moderation", map[string]any{"action": "unban", "memberId": uuid.New()}},
		{admin, http.MethodGet, "/staff/chat/rooms", nil},
		{admin, http.MethodPut, "/staff/chat/rooms/" + room + "/mentors/" + mentor.String(), nil},
		{admin, http.MethodDelete, "/staff/chat/rooms/" + room + "/mentors/" + mentor.String(), nil},
		{admin, http.MethodPut, "/staff/chat/messages/" + msg + "/legal-hold", nil},
		{admin, http.MethodDelete, "/staff/chat/messages/" + msg + "/legal-hold", nil},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			if st, _, raw := e.do(c.as, c.method, c.path, c.body); st != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500: %s", st, raw)
			}
		})
	}
}

// The generated router always decodes a body before calling the handler;
// a nil body only reaches the handler when it is called directly.
func TestHandlersRejectMissingBodies(t *testing.T) {
	s := &Server{Service: &service.Service{}}
	ctx := context.Background()
	id := uuid.New()
	if r, _ := s.SetRoomMute(ctx, SetRoomMuteRequestObject{RoomId: id}); r.(SetRoomMutedefaultApplicationProblemPlusJSONResponse).StatusCode != http.StatusBadRequest {
		t.Fatal("mute without body")
	}
	if r, _ := s.SendMessage(ctx, SendMessageRequestObject{RoomId: id}); r.(SendMessagedefaultApplicationProblemPlusJSONResponse).StatusCode != http.StatusBadRequest {
		t.Fatal("send without body")
	}
	if r, _ := s.ReportMessage(ctx, ReportMessageRequestObject{MessageId: id}); r.(ReportMessagedefaultApplicationProblemPlusJSONResponse).StatusCode != http.StatusBadRequest {
		t.Fatal("report without body")
	}
	if r, _ := s.ModerateRoom(ctx, ModerateRoomRequestObject{RoomId: id}); r.(ModerateRoomdefaultApplicationProblemPlusJSONResponse).StatusCode != http.StatusBadRequest {
		t.Fatal("moderation without body")
	}
}
