package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/api"
	"birge/internal/domains/emergency/emergencytest"
	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/service"
	"birge/internal/platform/auth"
)

const (
	fakePhone = "+70000000001"
	fakeCard  = "example child card"
)

type harness struct {
	h       http.Handler
	members *emergencytest.FakeMembers
	svc     *service.Service
}

func newHarness() *harness {
	members := emergencytest.NewFakeMembers()
	svc := &service.Service{
		Store:         emergencytest.NewMemoryStore(),
		Sealer:        &emergencytest.FakeSealer{},
		Members:       members,
		Blocks:        members,
		Acquaintances: members,
		Clock:         &emergencytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings:      service.Settings{PlanLinkKey: []byte("test-plan-link-key-0123456789abcdef")},
	}
	return &harness{h: api.Handler(api.NewStrictHandler(&api.Server{Service: svc}, nil)), members: members, svc: svc}
}

func (hs *harness) member(verified bool, district *uuid.UUID) *auth.Principal {
	p := &auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	hs.members.Put(p.MemberID, model.Profile{Verified: verified, DistrictID: district, DisplayName: "Example", Lang: "ru"})
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

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

const key = "Idempotency-Key"

func TestSosOverHTTP(t *testing.T) {
	hs := newHarness()
	d := uuid.New()
	m := hs.member(true, &d)

	if rec := hs.do(t, &auth.Principal{IdentityID: uuid.New(), Staff: true}, http.MethodPost, "/me/sos", `{"type":"other","text":"x"}`, key, uuid.NewString()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("staff raise = %d", rec.Code)
	}
	if rec := hs.do(t, m, http.MethodPost, "/me/sos", `{"type":"feel_bad","text":"x"}`, key, uuid.NewString()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad type = %d", rec.Code)
	}
	rec := hs.do(t, m, http.MethodPost, "/me/sos", `{"type":"childcare","text":"example text"}`, key, uuid.NewString())
	if rec.Code != http.StatusCreated {
		t.Fatalf("raise = %d %s", rec.Code, rec.Body)
	}
	child := decode[api.SosRequest](t, rec)
	if child.Text != "example text" || child.Status != api.Open || child.ClosesAt == nil {
		t.Fatalf("sos = %+v", child)
	}
	if rec := hs.do(t, m, http.MethodPost, "/me/sos/"+child.Id.String()+"/share", ""); rec.Code != http.StatusConflict {
		t.Fatalf("share childcare = %d", rec.Code)
	}
	rec = hs.do(t, m, http.MethodPost, "/me/sos", `{"type":"other","text":"example text"}`, key, uuid.NewString())
	other := decode[api.SosRequest](t, rec)
	if rec := hs.do(t, m, http.MethodPost, "/me/sos/"+other.Id.String()+"/share", ""); rec.Code != http.StatusOK || !decode[api.SosRequest](t, rec).SharedToDistrict {
		t.Fatalf("share = %d %s", rec.Code, rec.Body)
	}
	hs.do(t, m, http.MethodPost, "/me/sos", `{"type":"other","text":"example text"}`, key, uuid.NewString())
	if rec := hs.do(t, m, http.MethodPost, "/me/sos", `{"type":"other","text":"example text"}`, key, uuid.NewString()); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th raise = %d", rec.Code)
	}
	if list := decode[[]api.SosRequest](t, hs.do(t, m, http.MethodGet, "/me/sos", "")); len(list) != 3 {
		t.Fatalf("my sos = %d", len(list))
	}

	// Feed: verified only; respond is 204 and idempotent.
	if rec := hs.do(t, hs.member(false, &d), http.MethodGet, "/sos/feed", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("unverified feed = %d", rec.Code)
	}
	viewer := hs.member(true, &d)
	page := decode[api.SosFeedPage](t, hs.do(t, viewer, http.MethodGet, "/sos/feed", ""))
	if len(page.Items) != 1 || page.Items[0].Id != other.Id {
		t.Fatalf("feed = %+v", page)
	}
	for range 2 {
		if rec := hs.do(t, viewer, http.MethodPost, "/sos/"+other.Id.String()+"/respond", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("respond = %d %s", rec.Code, rec.Body)
		}
	}
	if rec := hs.do(t, hs.member(false, &d), http.MethodPost, "/sos/"+other.Id.String()+"/respond", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("unverified respond = %d", rec.Code)
	}
	resps := decode[[]api.SosResponse](t, hs.do(t, m, http.MethodGet, "/me/sos/"+other.Id.String()+"/responses", ""))
	if len(resps) != 1 || resps[0].MemberId != viewer.MemberID {
		t.Fatalf("responses = %+v", resps)
	}
	if rec := hs.do(t, m, http.MethodPost, "/me/sos/"+other.Id.String()+"/responses/"+resps[0].Id.String()+"/accept", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("accept = %d", rec.Code)
	}
	if rec := hs.do(t, viewer, http.MethodPost, "/me/sos/"+other.Id.String()+"/close", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("close another's = %d", rec.Code)
	}
	if rec := hs.do(t, m, http.MethodPost, "/me/sos/"+other.Id.String()+"/close", ""); rec.Code != http.StatusOK || decode[api.SosRequest](t, rec).Status != api.Closed {
		t.Fatalf("close = %d", rec.Code)
	}
}

func TestPlanOverHTTP(t *testing.T) {
	hs := newHarness()
	owner := hs.member(false, nil)
	friend := hs.member(true, nil)
	hs.members.Meet(owner.MemberID, friend.MemberID)

	if rec := hs.do(t, owner, http.MethodPut, "/me/emergency-plan/child-card", `{"text":"`+fakeCard+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("set card = %d %s", rec.Code, rec.Body)
	}
	rec := hs.do(t, owner, http.MethodPost, "/me/trusted-contacts", `{"kind":"external","name":"Example Contact","phone":"`+fakePhone+`"}`, key, uuid.NewString())
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), fakePhone) {
		t.Fatalf("add external = %d %s", rec.Code, rec.Body)
	}
	ext := decode[api.TrustedContact](t, rec)
	if ext.PhoneMasked == nil || *ext.PhoneMasked != "+7 *** *** 00 01" || ext.Status != api.Confirmed {
		t.Fatalf("external = %+v", ext)
	}
	if rec := hs.do(t, owner, http.MethodPost, "/me/trusted-contacts", `{"kind":"member","name":"x"}`, key, uuid.NewString()); rec.Code != http.StatusBadRequest {
		t.Fatalf("member without id = %d", rec.Code)
	}
	rec = hs.do(t, owner, http.MethodPost, "/me/trusted-contacts", `{"kind":"member","memberId":"`+friend.MemberID.String()+`"}`, key, uuid.NewString())
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d %s", rec.Code, rec.Body)
	}
	inv := decode[api.TrustedContact](t, rec)
	if rec := hs.do(t, friend, http.MethodPost, "/me/trusted-invitations/"+inv.Id.String()+"/accept", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("accept = %d", rec.Code)
	}
	if rec := hs.do(t, friend, http.MethodGet, "/trusted/plans/"+owner.MemberID.String(), ""); rec.Code != http.StatusForbidden {
		t.Fatalf("shared plan before send = %d", rec.Code)
	}
	if rec := hs.do(t, owner, http.MethodPost, "/me/emergency-plan/send", `{"confirm":"NO"}`, key, uuid.NewString()); rec.Code != http.StatusBadRequest {
		t.Fatalf("send without SEND = %d", rec.Code)
	}
	if rec := hs.do(t, owner, http.MethodPost, "/me/emergency-plan/send", `{"confirm":"SEND"}`, key, uuid.NewString()); rec.Code != http.StatusAccepted {
		t.Fatalf("send = %d %s", rec.Code, rec.Body)
	}
	rec = hs.do(t, friend, http.MethodGet, "/trusted/plans/"+owner.MemberID.String(), "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("shared plan = %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if sp := decode[api.SharedPlan](t, rec); sp.ChildCard != fakeCard {
		t.Fatalf("shared plan = %+v", sp)
	}
	rec = hs.do(t, owner, http.MethodGet, "/me/emergency-plan", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), fakePhone) {
		t.Fatalf("plan = %d %s", rec.Code, rec.Body)
	}
	plan := decode[api.EmergencyPlan](t, rec)
	if plan.ChildCard != fakeCard || len(plan.Contacts) != 2 || plan.LastSentAt == nil {
		t.Fatalf("plan = %+v", plan)
	}
	if rec := hs.do(t, owner, http.MethodDelete, "/me/trusted-contacts/"+ext.Id.String(), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("remove = %d", rec.Code)
	}
	if rec := hs.do(t, nil, http.MethodGet, "/me/emergency-plan", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rec.Code)
	}
}

// TestExternalPlanOverHTTP: the SMS link opens the card without a principal,
// with Cache-Control: no-store; unknown, malformed and revoked tokens get the
// very same 404 problem body.
func TestExternalPlanOverHTTP(t *testing.T) {
	hs := newHarness()
	owner := hs.member(false, nil)
	hs.do(t, owner, http.MethodPut, "/me/emergency-plan/child-card", `{"text":"`+fakeCard+`"}`)
	ext := decode[api.TrustedContact](t, hs.do(t, owner, http.MethodPost, "/me/trusted-contacts",
		`{"kind":"external","name":"Example Contact","phone":"`+fakePhone+`"}`, key, uuid.NewString()))
	if rec := hs.do(t, owner, http.MethodPost, "/me/emergency-plan/send", `{"confirm":"SEND"}`, key, uuid.NewString()); rec.Code != http.StatusAccepted {
		t.Fatalf("send = %d", rec.Code)
	}
	token, err := hs.svc.ExternalPlanLink(context.Background(), ext.Id)
	if err != nil {
		t.Fatal(err)
	}
	rec := hs.do(t, nil, http.MethodPost, "/external/plans/view", `{"token":"`+token+`"}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("view = %d %q %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
	}
	if sp := decode[api.SharedPlan](t, rec); sp.ChildCard != fakeCard || sp.OwnerDisplayName != "Example" {
		t.Fatalf("shared plan = %+v", sp)
	}

	unknown := hs.do(t, nil, http.MethodPost, "/external/plans/view", `{"token":"`+strings.Repeat("A", 43)+`"}`)
	malformed := hs.do(t, nil, http.MethodPost, "/external/plans/view", `{"token":"short"}`)
	if rec := hs.do(t, owner, http.MethodDelete, "/me/trusted-contacts/"+ext.Id.String(), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("remove = %d", rec.Code)
	}
	revoked := hs.do(t, nil, http.MethodPost, "/external/plans/view", `{"token":"`+token+`"}`)
	for name, r := range map[string]*httptest.ResponseRecorder{"unknown": unknown, "malformed": malformed, "revoked": revoked} {
		if r.Code != http.StatusNotFound || r.Body.String() != unknown.Body.String() {
			t.Errorf("%s = %d %s; want the unknown-token 404 %s", name, r.Code, r.Body, unknown.Body)
		}
		if strings.Contains(r.Body.String(), token) || strings.Contains(r.Body.String(), fakeCard) {
			t.Errorf("%s leaks the token or the card: %s", name, r.Body)
		}
	}
}
