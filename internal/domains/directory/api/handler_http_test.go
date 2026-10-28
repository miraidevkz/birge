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

	"birge/internal/domains/directory/directorytest"
	"birge/internal/domains/directory/model"
	"birge/internal/domains/directory/service"
	"birge/internal/platform/auth"
)

type httpEnv struct {
	t       *testing.T
	h       http.Handler
	as      auth.Principal
	svc     *service.Service
	store   *directorytest.MemoryStore
	members *directorytest.FakeMembers
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	e := &httpEnv{t: t, store: directorytest.NewMemoryStore(), members: directorytest.NewFakeMembers()}
	e.svc = &service.Service{Store: e.store, Members: e.members,
		Clock:  &directorytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	who := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), e.as)))
		})
	}
	e.h = HandlerWithOptions(NewStrictHandler(&Server{Service: e.svc}, nil), StdHTTPServerOptions{Middlewares: []MiddlewareFunc{who}})
	return e
}

func (e *httpEnv) do(p auth.Principal, method, path string, body any, header map[string]string) (int, map[string]any, []byte) {
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
	for k, v := range header {
		req.Header.Set(k, v)
	}
	e.as = p
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var obj map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &obj)
	return rec.Code, obj, rec.Body.Bytes()
}

func (e *httpEnv) member(verified bool, name string) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Put(p.MemberID, model.Profile{Verified: verified, DisplayName: name})
	return p
}

func TestHandlersMapProblems(t *testing.T) {
	e := newHTTPEnv(t)
	ctx := context.Background()
	verified, unverified := e.member(true, "Aigul Fakelast"), e.member(false, "Dana")
	staff := auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"admin"}, MFA: true}
	active, err := e.svc.SuggestSpecialist(ctx, verified, uuid.Must(uuid.NewV7()), model.NewSpecialist{
		Name: "Fake Clinic", Profession: "psychologist", DistrictID: uuid.New(), Needs: []string{"autism"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{Kind: model.ModerationKindSpecialist, ObjectID: active.ID, Approve: true}); err != nil {
		t.Fatal(err)
	}
	key := map[string]string{"Idempotency-Key": uuid.Must(uuid.NewV7()).String()}
	suggest := map[string]any{"name": "Fake Center", "profession": "speech_therapist", "districtId": uuid.New(), "needs": []string{"adhd"}, "contacts": "+7 700 000 0000"}
	review := func(id uuid.UUID) string { return "/specialists/" + id.String() + "/reviews" }

	cases := []struct {
		name   string
		as     auth.Principal
		method string
		path   string
		body   any
		header map[string]string
		status int
		code   string
	}{
		{"search", unverified, http.MethodGet, "/specialists?profession=psychologist&need=autism", nil, nil, 200, ""},
		{"search as staff", staff, http.MethodGet, "/specialists", nil, nil, 401, "unauthorized"},
		{"search bad profession", verified, http.MethodGet, "/specialists?profession=Bad", nil, nil, 400, "invalid_request"},
		{"search bad cursor", verified, http.MethodGet, "/specialists?cursor=@@", nil, nil, 400, "invalid_request"},
		{"suggest", verified, http.MethodPost, "/specialists", suggest, key, 201, ""},
		{"suggest replay", verified, http.MethodPost, "/specialists", suggest, key, 201, ""},
		{"suggest unverified", unverified, http.MethodPost, "/specialists", suggest, key, 403, "forbidden"},
		{"suggest anonymous", auth.Principal{}, http.MethodPost, "/specialists", suggest, key, 401, "unauthorized"},
		{"suggest invalid", verified, http.MethodPost, "/specialists", map[string]any{"name": "x", "profession": "psychologist", "districtId": uuid.New(), "needs": []string{}}, key, 400, "invalid_request"},
		{"review", verified, http.MethodPut, review(active.ID), map[string]any{"rating": 5, "text": "fake review"}, nil, 200, ""},
		{"review unverified", unverified, http.MethodPut, review(active.ID), map[string]any{"rating": 5, "text": "fake review"}, nil, 403, "forbidden"},
		{"review unknown specialist", verified, http.MethodPut, review(uuid.New()), map[string]any{"rating": 5, "text": "fake review"}, nil, 404, "not_found"},
		{"review bad rating", verified, http.MethodPut, review(active.ID), map[string]any{"rating": 9, "text": "fake review"}, nil, 400, "invalid_request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, body, raw := e.do(c.as, c.method, c.path, c.body, c.header)
			if status != c.status {
				t.Fatalf("status %d, want %d: %s", status, c.status, raw)
			}
			if c.code != "" && (body["code"] != c.code || body["status"] != float64(c.status)) {
				t.Fatalf("problem %s, want %s", raw, c.code)
			}
		})
	}

	// The suggested card is pending: it is not listed, and the replay made one card.
	_, body, raw := e.do(verified, http.MethodGet, "/specialists", nil, nil)
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("search %s", raw)
	}
	card := items[0].(map[string]any)
	if card["id"] != active.ID.String() || card["status"] != "active" || card["reviewCount"] != float64(0) || card["averageRating"] != nil {
		t.Fatalf("card %v", card)
	}
	if lr, ok := card["latestReviews"].([]any); !ok || len(lr) != 0 {
		t.Fatalf("latestReviews must be an empty list: %v", card["latestReviews"])
	}
	if n := len(e.store.EventsOfType(model.EventItemSubmitted)); n != 3 { // active, suggest (once), review
		t.Fatalf("submitted events %d, want 3", n)
	}
}

func TestReviewBodyAndSuggestBody(t *testing.T) {
	e := newHTTPEnv(t)
	ctx := context.Background()
	p := e.member(true, "Aigul Fakelast")
	sp, err := e.svc.SuggestSpecialist(ctx, p, uuid.Must(uuid.NewV7()), model.NewSpecialist{Name: "Fake Clinic", Profession: "psychologist", DistrictID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	st, body, raw := e.do(p, http.MethodPut, "/specialists/"+sp.ID.String()+"/reviews", map[string]any{"rating": 4, "text": " fake review "}, nil)
	if st != 200 || body["authorName"] != "Aigul" || body["status"] != "pending" || body["text"] != "fake review" || body["rating"] != float64(4) {
		t.Fatalf("review %d %s", st, raw)
	}
	st, body, raw = e.do(p, http.MethodPost, "/specialists", map[string]any{"name": "Fake Center", "profession": "lawyer", "districtId": uuid.New(), "needs": nil},
		map[string]string{"Idempotency-Key": uuid.Must(uuid.NewV7()).String()})
	if st != 201 || body["status"] != "pending" {
		t.Fatalf("suggest %d %s", st, raw)
	}
	if needs, ok := body["needs"].([]any); !ok || len(needs) != 0 {
		t.Fatalf("needs must be an empty list: %s", raw)
	}
}

func TestUnexpectedErrorsReach500(t *testing.T) {
	e := newHTTPEnv(t)
	p := e.member(true, "Aigul")
	e.store.FailOn = func(string) error { return errors.New("database down") }
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/specialists", nil},
		{http.MethodPost, "/specialists", map[string]any{"name": "Fake Center", "profession": "lawyer", "districtId": uuid.New(), "needs": []string{}}},
		{http.MethodPut, "/specialists/" + uuid.NewString() + "/reviews", map[string]any{"rating": 4, "text": "fake"}},
	} {
		st, _, raw := e.do(p, c.method, c.path, c.body, map[string]string{"Idempotency-Key": uuid.Must(uuid.NewV7()).String()})
		if st != 500 {
			t.Fatalf("%s %s: %d %s", c.method, c.path, st, raw)
		}
	}
}

func TestHandlersRejectMissingBodies(t *testing.T) {
	s := &Server{Service: &service.Service{}}
	ctx := context.Background()
	r1, _ := s.SuggestSpecialist(ctx, SuggestSpecialistRequestObject{})
	if got, ok := r1.(SuggestSpecialistdefaultApplicationProblemPlusJSONResponse); !ok || got.StatusCode != http.StatusBadRequest {
		t.Fatalf("suggest %#v", r1)
	}
	r2, _ := s.PutReview(ctx, PutReviewRequestObject{SpecialistId: uuid.New()})
	if got, ok := r2.(PutReviewdefaultApplicationProblemPlusJSONResponse); !ok || got.StatusCode != http.StatusBadRequest {
		t.Fatalf("review %#v", r2)
	}
}

func TestProblemForEveryError(t *testing.T) {
	for err, want := range map[error]int{
		model.ErrUnauthorized: 401, model.ErrForbidden: 403, model.ErrNotFound: 404,
		model.ErrInvalidInput: 400, model.ErrConflict: 409,
	} {
		if st, _, _, ok := problemFor(errors.Join(errors.New("wrapped"), err)); !ok || st != want {
			t.Errorf("%v: %d %v", err, st, ok)
		}
	}
	if _, _, _, ok := problemFor(errors.New("database down")); ok {
		t.Error("internal error mapped to a problem")
	}
}
