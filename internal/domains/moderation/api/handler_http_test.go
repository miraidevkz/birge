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
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/moderationtest"
	"birge/internal/domains/moderation/port"
	"birge/internal/domains/moderation/service"
	"birge/internal/platform/auth"
)

type httpEnv struct {
	t       *testing.T
	h       http.Handler
	as      auth.Principal
	svc     *service.Service
	store   *moderationtest.MemStore
	sources map[model.Kind]*moderationtest.FakeSource
	clock   *moderationtest.Clock
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	e := &httpEnv{t: t, store: moderationtest.NewMemStore(), sources: map[model.Kind]*moderationtest.FakeSource{},
		clock: &moderationtest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}}
	srcs := map[model.Kind]port.ContentSource{}
	for _, k := range model.Kinds {
		e.sources[k] = moderationtest.NewFakeSource()
		srcs[k] = e.sources[k]
	}
	e.svc = &service.Service{Store: e.store, Sources: srcs, Clock: e.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	who := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), e.as)))
		})
	}
	e.h = HandlerWithOptions(NewStrictHandler(&Server{Service: e.svc}, nil), StdHTTPServerOptions{Middlewares: []MiddlewareFunc{who}})
	return e
}

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
	_ = json.Unmarshal(rec.Body.Bytes(), &obj)
	return rec.Code, obj, rec.Body.Bytes()
}

func (e *httpEnv) open(kind model.Kind, reason string, sum model.Summary) uuid.UUID {
	e.t.Helper()
	obj := uuid.New()
	e.sources[kind].Put(obj, sum)
	if err := e.svc.Submit(context.Background(), uuid.Must(uuid.NewV7()), model.Submission{Kind: kind, ObjectID: obj, SourceReason: reason}); err != nil {
		e.t.Fatal(err)
	}
	e.clock.Advance(time.Second)
	for _, it := range e.store.Items() {
		if it.ObjectID == obj {
			return it.ID
		}
	}
	e.t.Fatal("no item")
	return uuid.Nil
}

func staffP(mfa bool, roles ...string) auth.Principal {
	return auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: roles, MFA: mfa}
}

func TestQueueAndDecisionStatusMapping(t *testing.T) {
	e := newHTTPEnv(t)
	listing := e.open(model.KindListing, "", model.Summary{Text: "fake listing", PhotoURL: "https://example.invalid/p.jpg"})
	report := e.open(model.KindReport, "harassment", model.Summary{Text: "fake message"})
	held := e.open(model.KindChatMessage, "link", model.Summary{Text: "fake held"})
	moderator, admin := staffP(true, "moderator"), staffP(true, "admin")
	decide := func(id uuid.UUID) string { return "/staff/moderation/items/" + id.String() + "/decision" }

	cases := []struct {
		name   string
		as     auth.Principal
		method string
		path   string
		body   any
		status int
		code   string
	}{
		{"queue as member", auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}, http.MethodGet, "/staff/moderation/queue", nil, 401, "unauthorized"},
		{"queue without MFA", staffP(false, "admin"), http.MethodGet, "/staff/moderation/queue", nil, 403, "forbidden"},
		{"queue as support", staffP(true, "support"), http.MethodGet, "/staff/moderation/queue", nil, 403, "forbidden"},
		{"queue unknown kind", moderator, http.MethodGet, "/staff/moderation/queue?kind=poll", nil, 400, "invalid_request"},
		{"queue bad cursor", moderator, http.MethodGet, "/staff/moderation/queue?cursor=" + url.QueryEscape("!!"), nil, 400, "invalid_request"},
		{"queue", moderator, http.MethodGet, "/staff/moderation/queue", nil, 200, ""},
		{"decide without MFA", staffP(false, "moderator"), http.MethodPost, decide(listing), map[string]any{"approve": true}, 403, "forbidden"},
		{"decide as member", auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}, http.MethodPost, decide(listing), map[string]any{"approve": true}, 401, "unauthorized"},
		{"decide unknown reason", moderator, http.MethodPost, decide(listing), map[string]any{"approve": false, "reason": "boring"}, 400, "invalid_request"},
		{"decide reject without reason", moderator, http.MethodPost, decide(listing), map[string]any{"approve": false}, 400, "invalid_request"},
		{"decide block author of a listing", moderator, http.MethodPost, decide(listing), map[string]any{"approve": false, "reason": "spam", "blockAuthor": true}, 400, "invalid_request"},
		{"decide unknown item", moderator, http.MethodPost, decide(uuid.New()), map[string]any{"approve": true}, 404, "not_found"},
		{"decide", moderator, http.MethodPost, decide(listing), map[string]any{"approve": true}, 204, ""},
		{"decide again", admin, http.MethodPost, decide(listing), map[string]any{"approve": false, "reason": "spam"}, 409, "already_decided"},
		{"reject report with block and hold", admin, http.MethodPost, decide(report),
			map[string]any{"approve": false, "reason": "unsafe", "blockAuthor": true, "legalHold": true}, 204, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, body, raw := e.do(c.as, c.method, c.path, c.body)
			if status != c.status {
				t.Fatalf("status %d, want %d: %s", status, c.status, raw)
			}
			if c.code != "" && (body["code"] != c.code || body["status"] != float64(c.status)) {
				t.Fatalf("problem %s, want %s", raw, c.code)
			}
		})
	}
	if it, _ := e.store.Item(report); !it.Decision.BlockAuthor || !it.Decision.LegalHold || it.Decision.Reason != "unsafe" {
		t.Fatalf("report decision %+v", it.Decision)
	}

	// The remaining open item is shown with its hold reason.
	_, body, raw := e.do(moderator, http.MethodGet, "/staff/moderation/queue?kind=chat_message", nil)
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("queue %s", raw)
	}
	it := items[0].(map[string]any)
	if it["id"] != held.String() || it["holdReason"] != "link" || it["summary"] != "fake held" || it["reportCount"] != nil || it["photoUrl"] != nil {
		t.Fatalf("held item %v", it)
	}
}

func TestQueueItemFieldsAndPaging(t *testing.T) {
	e := newHTTPEnv(t)
	report := e.open(model.KindReport, "spam", model.Summary{Text: "fake message"})
	if err := e.svc.Submit(context.Background(), uuid.Must(uuid.NewV7()), model.Submission{Kind: model.KindReport, ObjectID: mustItem(t, e, report).ObjectID}); err != nil {
		t.Fatal(err)
	}
	e.open(model.KindListing, "", model.Summary{Text: "fake listing", PhotoURL: "https://example.invalid/p.jpg"})
	for range model.PageSize {
		e.open(model.KindVerification, "", model.Summary{Text: "Fakename, fake district"})
	}
	moderator := staffP(true, "moderator")
	_, body, raw := e.do(moderator, http.MethodGet, "/staff/moderation/queue", nil)
	items, _ := body["items"].([]any)
	if len(items) != model.PageSize || body["nextCursor"] == nil {
		t.Fatalf("first page: %d items, cursor %v", len(items), body["nextCursor"])
	}
	first := items[0].(map[string]any)
	if first["kind"] != "report" || first["reportReason"] != "spam" || first["reportCount"] != float64(2) || first["holdReason"] != nil {
		t.Fatalf("report item %v (%s)", first, raw)
	}
	if second := items[1].(map[string]any); second["photoUrl"] != "https://example.invalid/p.jpg" {
		t.Fatalf("listing item %v", second)
	}
	_, body, raw = e.do(moderator, http.MethodGet, "/staff/moderation/queue?cursor="+url.QueryEscape(body["nextCursor"].(string)), nil)
	if items, _ := body["items"].([]any); len(items) != 2 || body["nextCursor"] != nil {
		t.Fatalf("second page %s", raw)
	}
}

func mustItem(t *testing.T, e *httpEnv, id uuid.UUID) model.Item {
	t.Helper()
	it, ok := e.store.Item(id)
	if !ok {
		t.Fatal("no item")
	}
	return it
}

func TestUnexpectedErrorsReach500(t *testing.T) {
	e := newHTTPEnv(t)
	moderator := staffP(true, "moderator")
	e.store.FailOn("InTx", errors.New("database down"))
	if st, _, raw := e.do(moderator, http.MethodGet, "/staff/moderation/queue", nil); st != 500 {
		t.Fatalf("queue: %d %s", st, raw)
	}
	if st, _, raw := e.do(moderator, http.MethodPost, "/staff/moderation/items/"+uuid.NewString()+"/decision", map[string]any{"approve": true}); st != 500 {
		t.Fatalf("decide: %d %s", st, raw)
	}
	// A source outage is not a client error either.
	e.store.FailOn("InTx", nil)
	e.open(model.KindListing, "", model.Summary{Text: "fake"})
	e.sources[model.KindListing].Fail = errors.New("sharing down")
	if st, _, raw := e.do(moderator, http.MethodGet, "/staff/moderation/queue", nil); st != 500 {
		t.Fatalf("source down: %d %s", st, raw)
	}
}

func TestDecideWithoutBody(t *testing.T) {
	s := &Server{Service: &service.Service{}}
	r, err := s.DecideModerationItem(context.Background(), DecideModerationItemRequestObject{ItemId: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.(DecideModerationItemdefaultApplicationProblemPlusJSONResponse); !ok || got.StatusCode != http.StatusBadRequest {
		t.Fatalf("response %#v", r)
	}
}
