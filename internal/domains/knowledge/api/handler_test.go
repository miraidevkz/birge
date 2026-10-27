package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/api"
	"birge/internal/domains/knowledge/knowledgetest"
	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/service"
	"birge/internal/platform/auth"
)

type harness struct {
	h     http.Handler
	clock *knowledgetest.Clock
}

func newHarness() *harness {
	clk := &knowledgetest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := &service.Service{
		Store:  knowledgetest.NewMemoryStore(),
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service.Settings{
			RightsTTL:   180 * 24 * time.Hour,
			SelfhelpTTL: 365 * 24 * time.Hour,
		},
	}
	return &harness{h: api.Handler(api.NewStrictHandler(&api.Server{Service: svc}, nil)), clock: clk}
}

func (hs *harness) do(t *testing.T, p *auth.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost {
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	if p != nil {
		req = req.WithContext(auth.WithPrincipal(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	return rec
}

func staff(role string) *auth.Principal {
	return &auth.Principal{IdentityID: uuid.New(), Staff: true, MFA: true, Roles: []string{role}}
}

func member() *auth.Principal {
	return &auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

// expect checks the status and, for problems, the stable problem code.
func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
	if code == "" {
		return
	}
	p := decode[api.Problem](t, rec)
	if p.Code == nil || *p.Code != code {
		t.Fatalf("problem code = %v, want %q; body %s", p.Code, code, rec.Body.String())
	}
}

// draftBody is a minimal self-help draft in Russian for topicID.
func draftBody(topicID uuid.UUID) string {
	return `{"topicId":"` + topicID.String() + `","lang":"ru","content":{"title":"Сон","body":"Короткие советы."}}`
}

func createTopic(t *testing.T, hs *harness, p *auth.Principal, kind, code string) api.KnowledgeTopic {
	t.Helper()
	rec := hs.do(t, p, http.MethodPost, "/staff/knowledge/topics",
		`{"kind":"`+kind+`","code":"`+code+`","titles":{"ru":"Тема"}}`)
	expect(t, rec, http.StatusCreated, "")
	return decode[api.KnowledgeTopic](t, rec)
}

func TestSelfhelpArticleFlowOverHTTP(t *testing.T) {
	hs := newHarness()
	author, reviewer := staff(model.RolePsychologist), staff(model.RolePsychologist)
	topic := createTopic(t, hs, author, "selfhelp", "sleep")

	rec := hs.do(t, author, http.MethodPost, "/staff/knowledge/articles", draftBody(topic.Id))
	expect(t, rec, http.StatusCreated, "")
	draft := decode[api.StaffArticle](t, rec)
	if draft.Status != "draft" || draft.AuthorId != author.IdentityID {
		t.Fatalf("draft = %+v", draft)
	}
	vpath := "/staff/knowledge/versions/" + draft.VersionId.String()

	rec = hs.do(t, author, http.MethodPut, vpath, `{"title":"Сон ребёнка","body":"Советы."}`)
	expect(t, rec, http.StatusOK, "")
	if got := decode[api.StaffArticle](t, rec).Content.Title; got != "Сон ребёнка" {
		t.Fatalf("title after update = %q", got)
	}

	expect(t, hs.do(t, author, http.MethodPost, vpath+"/submit", ""), http.StatusOK, "")
	// The author never publishes their own version.
	expect(t, hs.do(t, author, http.MethodPost, vpath+"/publish", ""), http.StatusForbidden, "forbidden")
	// A lawyer reviews rights, not self-help.
	expect(t, hs.do(t, staff(model.RoleLawyer), http.MethodPost, vpath+"/publish", ""), http.StatusForbidden, "forbidden")

	rec = hs.do(t, reviewer, http.MethodPost, vpath+"/publish", "")
	expect(t, rec, http.StatusOK, "")
	pub := decode[api.StaffArticle](t, rec)
	if pub.Status != "published" || pub.ReviewerId == nil || *pub.ReviewerId != reviewer.IdentityID || pub.ExpiresAt == nil {
		t.Fatalf("published = %+v", pub)
	}
	// Publishing twice, or editing what is no longer a draft, is a state conflict.
	expect(t, hs.do(t, reviewer, http.MethodPost, vpath+"/publish", ""), http.StatusConflict, "conflict")
	expect(t, hs.do(t, author, http.MethodPut, vpath, `{"title":"x","body":"y"}`), http.StatusConflict, "conflict")

	// Members see the published article in their language.
	m := member()
	rec = hs.do(t, m, http.MethodGet, "/knowledge/topics?lang=ru&kind=selfhelp", "")
	expect(t, rec, http.StatusOK, "")
	if topics := decode[[]api.KnowledgeTopic](t, rec); len(topics) != 1 || topics[0].Title != "Тема" {
		t.Fatalf("topics = %+v", topics)
	}
	rec = hs.do(t, m, http.MethodGet, "/knowledge/topics/"+topic.Id.String()+"/articles?lang=ru", "")
	expect(t, rec, http.StatusOK, "")
	if list := decode[[]api.ArticleSummary](t, rec); len(list) != 1 || list[0].Id != pub.ArticleId {
		t.Fatalf("articles = %+v", list)
	}
	expect(t, hs.do(t, m, http.MethodGet, "/knowledge/articles/"+pub.ArticleId.String()+"?lang=ru", ""), http.StatusOK, "")

	// Staff list shows the version.
	rec = hs.do(t, reviewer, http.MethodGet, "/staff/knowledge/articles", "")
	expect(t, rec, http.StatusOK, "")
	if page := decode[api.StaffArticlePage](t, rec); len(page.Items) == 0 {
		t.Fatal("staff list is empty")
	}
}

func TestRejectOverHTTP(t *testing.T) {
	hs := newHarness()
	author := staff(model.RolePsychologist)
	topic := createTopic(t, hs, author, "selfhelp", "calm")
	rec := hs.do(t, author, http.MethodPost, "/staff/knowledge/articles", draftBody(topic.Id))
	expect(t, rec, http.StatusCreated, "")
	vpath := "/staff/knowledge/versions/" + decode[api.StaffArticle](t, rec).VersionId.String()
	expect(t, hs.do(t, author, http.MethodPost, vpath+"/submit", ""), http.StatusOK, "")

	reviewer := staff(model.RolePsychologist)
	// The reason is an enum of the contract.
	expect(t, hs.do(t, reviewer, http.MethodPost, vpath+"/reject", `{"reason":"because"}`), http.StatusBadRequest, "")
	rec = hs.do(t, reviewer, http.MethodPost, vpath+"/reject", `{"reason":"unsafe_advice"}`)
	expect(t, rec, http.StatusOK, "")
	got := decode[api.StaffArticle](t, rec)
	// A rejected version goes back to its author as a draft, with the reason.
	if got.Status != "draft" || got.RejectReason == nil || *got.RejectReason != "unsafe_advice" {
		t.Fatalf("rejected = %+v", got)
	}
}

func TestDenyByDefault(t *testing.T) {
	hs := newHarness()
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/staff/knowledge/topics", ""},
		{http.MethodPost, "/staff/knowledge/topics", `{"kind":"rights","code":"law","titles":{"ru":"Право"}}`},
		{http.MethodGet, "/staff/knowledge/articles", ""},
		{http.MethodGet, "/staff/knowledge/review-due", ""},
		{http.MethodPost, "/staff/knowledge/articles", draftBody(uuid.New())},
		// Unknown versions: deny comes before the lookup, so no 404 leaks existence.
		{http.MethodPost, "/staff/knowledge/versions/" + uuid.NewString() + "/publish", ""},
		{http.MethodPost, "/staff/knowledge/versions/" + uuid.NewString() + "/submit", ""},
		{http.MethodPost, "/staff/knowledge/versions/" + uuid.NewString() + "/reject", `{"reason":"unclear"}`},
		{http.MethodPut, "/staff/knowledge/versions/" + uuid.NewString(), `{"title":"x","body":"y"}`},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			// No principal at all, and a member principal, both get nothing.
			if rec := hs.do(t, nil, r.method, r.path, r.body); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
				t.Fatalf("anonymous: status = %d, want 401 or 403", rec.Code)
			}
			if rec := hs.do(t, member(), r.method, r.path, r.body); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
				t.Fatalf("member: status = %d, want 401 or 403", rec.Code)
			}
		})
	}
	// A moderator has no knowledge role.
	expect(t, hs.do(t, staff("moderator"), http.MethodPost, "/staff/knowledge/topics",
		`{"kind":"rights","code":"law","titles":{"ru":"Право"}}`), http.StatusForbidden, "forbidden")
}

func TestProblemsOverHTTP(t *testing.T) {
	hs := newHarness()
	psy := staff(model.RolePsychologist)
	unknown := "/staff/knowledge/versions/" + uuid.NewString()

	expect(t, hs.do(t, psy, http.MethodPost, unknown+"/submit", ""), http.StatusNotFound, "not_found")
	expect(t, hs.do(t, psy, http.MethodPut, unknown, `{"title":"x","body":"y"}`), http.StatusNotFound, "not_found")
	expect(t, hs.do(t, member(), http.MethodGet, "/knowledge/articles/"+uuid.NewString()+"?lang=ru", ""), http.StatusNotFound, "not_found")
	// A draft for a topic that does not exist.
	rec := hs.do(t, psy, http.MethodPost, "/staff/knowledge/articles", draftBody(uuid.New()))
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
		t.Fatalf("draft for unknown topic: status = %d", rec.Code)
	}
	// Malformed JSON and an invalid topic code are rejected at the boundary.
	expect(t, hs.do(t, psy, http.MethodPost, "/staff/knowledge/topics", `{`), http.StatusBadRequest, "")
	rec = hs.do(t, psy, http.MethodPost, "/staff/knowledge/topics", `{"kind":"selfhelp","code":"Bad Code!","titles":{"ru":"x"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid code: status = %d, want 400", rec.Code)
	}
	// A psychologist cannot open a rights topic.
	expect(t, hs.do(t, psy, http.MethodPost, "/staff/knowledge/topics",
		`{"kind":"rights","code":"law","titles":{"ru":"Право"}}`), http.StatusForbidden, "forbidden")
}

func TestReviewDueOverHTTP(t *testing.T) {
	hs := newHarness()
	author, reviewer := staff(model.RolePsychologist), staff(model.RolePsychologist)
	topic := createTopic(t, hs, author, "selfhelp", "rest")
	rec := hs.do(t, author, http.MethodPost, "/staff/knowledge/articles", draftBody(topic.Id))
	expect(t, rec, http.StatusCreated, "")
	vpath := "/staff/knowledge/versions/" + decode[api.StaffArticle](t, rec).VersionId.String()
	expect(t, hs.do(t, author, http.MethodPost, vpath+"/submit", ""), http.StatusOK, "")
	expect(t, hs.do(t, reviewer, http.MethodPost, vpath+"/publish", ""), http.StatusOK, "")

	rec = hs.do(t, reviewer, http.MethodGet, "/staff/knowledge/review-due", "")
	expect(t, rec, http.StatusOK, "")
	if page := decode[api.StaffArticlePage](t, rec); len(page.Items) != 0 {
		t.Fatalf("review-due before any timer fired = %+v", page.Items)
	}
}
