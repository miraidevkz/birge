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

	"birge/internal/domains/safety/api"
	"birge/internal/domains/safety/model"
	"birge/internal/domains/safety/safetytest"
	"birge/internal/domains/safety/service"
	"birge/internal/platform/auth"
)

type harness struct {
	h     http.Handler
	dir   *safetytest.FakeDirectory
	clock *safetytest.Clock
}

func newHarness() *harness {
	dir := &safetytest.FakeDirectory{Contacts: map[uuid.UUID]model.Contact{}, Districts: map[uuid.UUID]uuid.UUID{}}
	clk := &safetytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := &service.Service{
		Store:     safetytest.NewMemoryStore(),
		Sealer:    &safetytest.FakeSealer{},
		Directory: dir,
		Clock:     clk,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings:  service.Settings{CrisisPhones: []model.CrisisPhone{{Number: "000", Label: "Example line"}}},
	}
	h := api.Handler(api.NewStrictHandler(&api.Server{Service: svc}, nil))
	return &harness{h: h, dir: dir, clock: clk}
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

func staff(roles ...string) *auth.Principal {
	return &auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: roles}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestEscalationFlowOverHTTP(t *testing.T) {
	hs := newHarness()
	memberID := uuid.New()
	hs.dir.Contacts[memberID] = model.Contact{DisplayName: "Example Member", Lang: "kk", Phone: "+70000000001"}
	m := &auth.Principal{IdentityID: uuid.New(), MemberID: memberID}
	key := "Idempotency-Key"

	rec := hs.do(t, m, http.MethodPost, "/me/help-requests", `{"source":"menu"}`, key, uuid.NewString())
	if rec.Code != http.StatusAccepted {
		t.Fatalf("help = %d %s", rec.Code, rec.Body)
	}
	help := decode[api.HelpRequestAccepted](t, rec)
	if help.EscalationId == uuid.Nil || len(help.CrisisPhones) != 1 || help.CrisisPhones[0].Number != "000" {
		t.Fatalf("help body = %+v", help)
	}
	if rec := hs.do(t, m, http.MethodPost, "/me/help-requests", `{"source":"chat"}`, key, uuid.NewString()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad source = %d", rec.Code)
	}
	if rec := hs.do(t, staff(model.RolePsychologist), http.MethodPost, "/me/help-requests", `{"source":"menu"}`, key, uuid.NewString()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("staff help = %d", rec.Code)
	}

	// Queue: members and staff without a queue role are refused.
	if rec := hs.do(t, m, http.MethodGet, "/staff/escalations", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("member list = %d", rec.Code)
	}
	if rec := hs.do(t, nil, http.MethodGet, "/staff/escalations", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d", rec.Code)
	}
	psy := staff(model.RolePsychologist)
	rec = hs.do(t, psy, http.MethodGet, "/staff/escalations?status=open", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), memberID.String()) || strings.Contains(rec.Body.String(), "+7000") {
		t.Fatal("queue exposes member data")
	}
	page := decode[api.EscalationPage](t, rec)
	if len(page.Items) != 1 || page.Items[0].Id != help.EscalationId || page.Items[0].TakenByMe != nil {
		t.Fatalf("page = %+v", page)
	}

	escPath := "/staff/escalations/" + help.EscalationId.String()
	if rec := hs.do(t, psy, http.MethodPost, escPath+"/contact", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("reveal before take = %d", rec.Code)
	}
	rec = hs.do(t, psy, http.MethodPost, escPath+"/take", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("take = %d %s", rec.Code, rec.Body)
	}
	if e := decode[api.Escalation](t, rec); e.Status != api.EscalationStatusTaken || e.TakenByMe == nil || !*e.TakenByMe {
		t.Fatalf("taken = %+v", e)
	}
	if rec := hs.do(t, staff(model.RoleModerator), http.MethodPost, escPath+"/take", ""); rec.Code != http.StatusConflict {
		t.Fatalf("second taker = %d", rec.Code)
	}
	if rec := hs.do(t, staff(model.RolePsychologist), http.MethodPost, escPath+"/contact", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("reveal by other = %d", rec.Code)
	}
	rec = hs.do(t, psy, http.MethodPost, escPath+"/contact", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reveal = %d cache=%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if c := decode[api.EscalationContact](t, rec); c.DisplayName != "Example Member" || c.Phone != "+70000000001" || c.Lang != api.Kk {
		t.Fatalf("contact = %+v", c)
	}

	if rec := hs.do(t, psy, http.MethodPost, escPath+"/close", `{"outcome":"solved"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad outcome = %d", rec.Code)
	}
	rec = hs.do(t, psy, http.MethodPost, escPath+"/close", `{"outcome":"contacted","note":"example note"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("close = %d %s", rec.Code, rec.Body)
	}
	if e := decode[api.Escalation](t, rec); e.Status != api.EscalationStatusClosed || e.Outcome == nil || *e.Outcome != api.EscalationOutcomeContacted {
		t.Fatalf("closed = %+v", e)
	}
	if strings.Contains(rec.Body.String(), "example note") {
		t.Fatal("note echoed")
	}
	if rec := hs.do(t, psy, http.MethodPost, escPath+"/close", `{"outcome":"contacted"}`); rec.Code != http.StatusConflict {
		t.Fatalf("close twice = %d", rec.Code)
	}
	if rec := hs.do(t, psy, http.MethodPost, "/staff/escalations/"+uuid.NewString()+"/take", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("take unknown = %d", rec.Code)
	}
}

func TestDutyRosterOverHTTP(t *testing.T) {
	hs := newHarness()
	admin := staff(model.RoleAdmin)
	now := hs.clock.Now()
	body := func(start, end time.Time) string {
		return `{"staffId":"` + uuid.NewString() + `","role":"psychologist","backup":false,` +
			`"startsAt":"` + start.Format(time.RFC3339) + `","endsAt":"` + end.Format(time.RFC3339) + `","phone":"+70000000002"}`
	}
	key := uuid.Must(uuid.NewV7()).String()
	future := body(now.Add(24*time.Hour), now.Add(32*time.Hour))

	if rec := hs.do(t, staff(model.RolePsychologist), http.MethodPost, "/staff/duty-roster", future, "Idempotency-Key", key); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin add = %d", rec.Code)
	}
	rec := hs.do(t, admin, http.MethodPost, "/staff/duty-roster", future, "Idempotency-Key", key)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "+7000") {
		t.Fatal("phone returned")
	}
	shift := decode[api.DutyShift](t, rec)
	if shift.Id.String() != key {
		t.Fatalf("shift id %v, want the Idempotency-Key", shift.Id)
	}
	if rec := hs.do(t, admin, http.MethodPost, "/staff/duty-roster", future, "Idempotency-Key", key); rec.Code != http.StatusCreated {
		t.Fatalf("repeat add = %d", rec.Code)
	}
	bad := strings.Replace(future, "+70000000002", "12345", 1)
	if rec := hs.do(t, admin, http.MethodPost, "/staff/duty-roster", bad, "Idempotency-Key", uuid.NewString()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad phone = %d", rec.Code)
	}

	q := "/staff/duty-roster?from=" + now.Format(time.RFC3339) + "&to=" + now.Add(48*time.Hour).Format(time.RFC3339)
	rec = hs.do(t, staff(model.RoleModerator), http.MethodGet, q, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("roster = %d %s", rec.Code, rec.Body)
	}
	if list := decode[[]api.DutyShift](t, rec); len(list) != 1 || list[0].Id != shift.Id {
		t.Fatalf("roster = %+v", list)
	}

	started := body(now.Add(-time.Hour), now.Add(time.Hour))
	startedKey := uuid.NewString()
	if rec := hs.do(t, admin, http.MethodPost, "/staff/duty-roster", started, "Idempotency-Key", startedKey); rec.Code != http.StatusCreated {
		t.Fatalf("add started = %d", rec.Code)
	}
	if rec := hs.do(t, admin, http.MethodDelete, "/staff/duty-roster/"+startedKey, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete started = %d", rec.Code)
	}
	if rec := hs.do(t, staff(model.RolePsychologist), http.MethodDelete, "/staff/duty-roster/"+key, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin delete = %d", rec.Code)
	}
	if rec := hs.do(t, admin, http.MethodDelete, "/staff/duty-roster/"+key, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec := hs.do(t, admin, http.MethodDelete, "/staff/duty-roster/"+uuid.NewString(), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown = %d", rec.Code)
	}
}
