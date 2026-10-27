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

	"birge/internal/domains/circle/api"
	"birge/internal/domains/circle/circletest"
	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/service"
	"birge/internal/platform/auth"
)

type harness struct {
	h       http.Handler
	members *circletest.FakeMembers
}

func newHarness() *harness {
	members := circletest.NewFakeMembers()
	svc := &service.Service{
		Store: circletest.NewMemoryStore(), Members: members, Blocks: members,
		Clock:  &circletest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return &harness{h: api.Handler(api.NewStrictHandler(&api.Server{Service: svc}, nil)), members: members}
}

func (hs *harness) member(verified bool) *auth.Principal {
	p := &auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	d := uuid.New()
	hs.members.Put(p.MemberID, model.Profile{
		Verified: verified, DistrictID: &d, Lang: "ru", FirstName: "Example",
		Children: []model.Child{{BirthYear: 2018, Needs: []string{"autism"}}},
	})
	return p
}

func (hs *harness) do(t *testing.T, p *auth.Principal, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	if p != nil {
		req = req.WithContext(auth.WithPrincipal(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	return rec
}

func TestTeaOverHTTP(t *testing.T) {
	hs := newHarness()
	rec := hs.do(t, nil, http.MethodPost, "/me/tea-requests",
		`{"format":"meeting","sameDistrict":false,"similarAge":false,"sharedNeeds":false}`,
		"Idempotency-Key", uuid.New().String())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon=%d", rec.Code)
	}

	unverified := hs.member(false)
	rec = hs.do(t, unverified, http.MethodPost, "/me/tea-requests",
		`{"format":"meeting","sameDistrict":false,"similarAge":false,"sharedNeeds":false}`,
		"Idempotency-Key", uuid.New().String())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unverified=%d body=%s", rec.Code, rec.Body.String())
	}

	p := hs.member(true)
	rec = hs.do(t, p, http.MethodPost, "/me/tea-requests",
		`{"format":"meeting","sameDistrict":true,"similarAge":false,"sharedNeeds":false}`,
		"Idempotency-Key", uuid.New().String())
	if rec.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
	}
	var created api.TeaRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	rec = hs.do(t, p, http.MethodPost, "/me/tea-requests",
		`{"format":"call","sameDistrict":false,"similarAge":false,"sharedNeeds":false}`,
		"Idempotency-Key", uuid.New().String())
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflict=%d", rec.Code)
	}

	rec = hs.do(t, p, http.MethodGet, "/me/tea-requests", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list=%d", rec.Code)
	}

	rec = hs.do(t, p, http.MethodPost, "/me/tea-requests/"+created.Id.String()+"/cancel", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel=%d", rec.Code)
	}

	rec = hs.do(t, p, http.MethodGet, "/me/matches", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("matches=%d", rec.Code)
	}
	rec = hs.do(t, p, http.MethodGet, "/me/acquaintances", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("acquaintances=%d", rec.Code)
	}
}
