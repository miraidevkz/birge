package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	emergencyadapter "birge/internal/domains/emergency/adapter"
	emergencymodel "birge/internal/domains/emergency/model"
	emergencyservice "birge/internal/domains/emergency/service"
	notifymodel "birge/internal/domains/notify/model"
	"birge/internal/platform/clock"
	"birge/internal/platform/ratelimit"
)

// TestExternalPlanLinkEndToEnd runs the external plan link through the real
// HTTP stack: viewExternalPlan works without a session (security: []) while
// its emergency neighbours still answer 401; the link the worker would put
// in the SMS opens the card once; failures are one 404; the per-IP limit
// answers 429; and the token is never in the logs.
func TestExternalPlanLinkEndToEnd(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	cfg := e.cfg
	cfg.PlanLinkPerIP = ratelimit.Rate{Burst: 3, Every: time.Hour}
	h := e.handler(cfg)

	const card = "example child card for the link"
	if rec := do(t, h, http.MethodPut, "/v1/me/emergency-plan/child-card", e2eToken, `{"text":"`+card+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("set card: %d %s", rec.Code, rec.Body)
	}
	rec := do(t, h, http.MethodPost, "/v1/me/trusted-contacts", e2eToken, `{"kind":"external","name":"Example Contact","phone":"+77000000001"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add contact: %d %s", rec.Code, rec.Body)
	}
	var contact struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &contact); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, http.MethodPost, "/v1/me/emergency-plan/send", e2eToken, `{"confirm":"SEND"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	var owner uuid.UUID
	if err := e.pool.QueryRow(ctx, "SELECT id FROM membership.members WHERE identity_id = $1", e.identity).Scan(&owner); err != nil {
		t.Fatal(err)
	}

	// What the worker does when notify sends the SMS.
	links := &EmergencySMSLinks{
		Emergency: &emergencyservice.Service{
			Store: &emergencyadapter.PostgresStore{Pool: e.pool}, Sealer: e.envelope, Clock: clock.System{},
			Settings: emergencyservice.Settings{PlanLinkKey: cfg.PlanLinkKey},
		},
		PublicURL: "https://app.example.invalid",
	}
	if _, err := links.LinkFor(ctx, emergencymodel.TemplateTrustedInvitation, notifymodel.RecipientKindExternal, contact.ID, nil); !errors.Is(err, notifymodel.ErrNotFound) {
		t.Fatalf("link for another template: %v", err)
	}
	link, err := links.LinkFor(ctx, emergencymodel.TemplatePlanSent, notifymodel.RecipientKindExternal, contact.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, ok := strings.CutPrefix(link, "https://app.example.invalid/plan#")
	if !ok || len(token) != 43 {
		t.Fatalf("link shape: %d characters", len(link))
	}

	// No session: the view works; the neighbours do not.
	rec = do(t, h, http.MethodPost, "/v1/external/plans/view", "", `{"token":"`+token+`"}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), card) {
		t.Fatalf("view: %d %q %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
	}
	for _, r := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/trusted/plans/" + owner.String(), ""},
		{http.MethodGet, "/v1/me/emergency-plan", ""},
		{http.MethodPost, "/v1/me/emergency-plan/send", `{"confirm":"SEND"}`},
		{http.MethodGet, "/v1/external/plans/view", ""},
	} {
		if rec := do(t, h, r.method, r.path, "", r.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", r.method, r.path, rec.Code)
		}
	}
	if got := countRows(t, e, `SELECT count(*) FROM platform.audit_log WHERE action = $1 AND actor_id = $2 AND object_id = $3`,
		emergencymodel.AuditViewExternalPlan, contact.ID, owner); got != 1 {
		t.Fatalf("view audit rows = %d", got)
	}

	// Revoked (by a resend) and unknown tokens: the same 404.
	if rec := do(t, h, http.MethodPost, "/v1/me/emergency-plan/send", e2eToken, `{"confirm":"SEND"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body)
	}
	revoked := do(t, h, http.MethodPost, "/v1/external/plans/view", "", `{"token":"`+token+`"}`)
	unknown := do(t, h, http.MethodPost, "/v1/external/plans/view", "", `{"token":"`+strings.Repeat("A", 43)+`"}`)
	if revoked.Code != http.StatusNotFound || revoked.Body.String() != unknown.Body.String() {
		t.Fatalf("revoked %d %s vs unknown %d %s", revoked.Code, revoked.Body, unknown.Code, unknown.Body)
	}
	// The fourth view from this IP is over the limit.
	if rec := do(t, h, http.MethodPost, "/v1/external/plans/view", "", `{"token":"`+token+`"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %d", rec.Code)
	}

	if strings.Contains(e.logs.String(), token) {
		t.Fatal("logs contain the plan link token")
	}
	if got := countRows(t, e, `SELECT count(*) FROM platform.outbox WHERE payload::text LIKE '%' || $1 || '%'`, token); got != 0 {
		t.Fatal("outbox contains the plan link token")
	}
}

func countRows(t *testing.T, e *e2e, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestExternalSosLinkEndToEnd runs the external SOS link through the real
// HTTP stack: the link the worker puts in the SOS SMS opens the SOS without
// a session; a plan token does not; closing the SOS ends it with the same
// 404 as an unknown token; the SOS view has its own per-IP limit; the token
// is never in the logs or the outbox.
func TestExternalSosLinkEndToEnd(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	cfg := e.cfg
	cfg.PlanLinkPerIP = ratelimit.Rate{Burst: 3, Every: time.Hour}
	h := e.handler(cfg)

	const text = "example sos text for the link"
	rec := do(t, h, http.MethodPost, "/v1/me/trusted-contacts", e2eToken, `{"kind":"external","name":"Example Contact","phone":"+77000000001"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add contact: %d %s", rec.Code, rec.Body)
	}
	var contact, sos struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &contact); err != nil {
		t.Fatal(err)
	}
	rec = do(t, h, http.MethodPost, "/v1/me/sos", e2eToken, `{"type":"childcare","text":"`+text+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("raise: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sos); err != nil {
		t.Fatal(err)
	}

	// What the worker does when notify sends the SOS SMS.
	links := &EmergencySMSLinks{
		Emergency: &emergencyservice.Service{
			Store: &emergencyadapter.PostgresStore{Pool: e.pool}, Sealer: e.envelope, Clock: clock.System{},
			Settings: emergencyservice.Settings{PlanLinkKey: cfg.PlanLinkKey},
		},
		PublicURL: "https://app.example.invalid/",
	}
	if _, err := links.LinkFor(ctx, emergencymodel.TemplateSosRaisedContact, notifymodel.RecipientKindExternal, contact.ID, map[string]string{}); !errors.Is(err, notifymodel.ErrNotFound) {
		t.Fatalf("link without sos_id: %v", err)
	}
	if _, err := links.LinkFor(ctx, emergencymodel.TemplateSosRaisedContact, notifymodel.RecipientKindMember, contact.ID, map[string]string{"sos_id": sos.ID.String()}); !errors.Is(err, notifymodel.ErrNotFound) {
		t.Fatalf("link for a member recipient: %v", err)
	}
	link, err := links.LinkFor(ctx, emergencymodel.TemplateSosRaisedContact, notifymodel.RecipientKindExternal, contact.ID, map[string]string{"sos_id": sos.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	token, ok := strings.CutPrefix(link, "https://app.example.invalid/sos-link#")
	if !ok || len(token) != 43 {
		t.Fatalf("link shape: %d characters", len(link))
	}

	rec = do(t, h, http.MethodPost, "/v1/external/sos/view", "", `{"token":"`+token+`"}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), text) {
		t.Fatalf("view: %d %q %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
	}
	if rec := do(t, h, http.MethodGet, "/v1/external/sos/view", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET without a session: %d, want 401", rec.Code)
	}
	if got := countRows(t, e, `SELECT count(*) FROM platform.audit_log WHERE action = $1 AND actor_id = $2 AND object_id = $3 AND actor_role = $4`,
		emergencymodel.AuditViewExternalSos, contact.ID, sos.ID, emergencymodel.AuditActorExternalContact); got != 1 {
		t.Fatalf("view audit rows = %d", got)
	}
	// The SOS token does not open the plan page.
	if rec := do(t, h, http.MethodPost, "/v1/external/plans/view", "", `{"token":"`+token+`"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("sos token on the plan view: %d", rec.Code)
	}

	// Closed and unknown: the same 404.
	if rec := do(t, h, http.MethodPost, "/v1/me/sos/"+sos.ID.String()+"/close", e2eToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("close: %d %s", rec.Code, rec.Body)
	}
	closed := do(t, h, http.MethodPost, "/v1/external/sos/view", "", `{"token":"`+token+`"}`)
	unknown := do(t, h, http.MethodPost, "/v1/external/sos/view", "", `{"token":"`+strings.Repeat("A", 43)+`"}`)
	if closed.Code != http.StatusNotFound || closed.Body.String() != unknown.Body.String() {
		t.Fatalf("closed %d %s vs unknown %d %s", closed.Code, closed.Body, unknown.Code, unknown.Body)
	}
	// The fourth SOS view from this IP is over the limit (the plan view
	// above counted in its own bucket).
	if rec := do(t, h, http.MethodPost, "/v1/external/sos/view", "", `{"token":"`+token+`"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %d", rec.Code)
	}

	if strings.Contains(e.logs.String(), token) {
		t.Fatal("logs contain the sos link token")
	}
	if got := countRows(t, e, `SELECT count(*) FROM platform.outbox WHERE payload::text LIKE '%' || $1 || '%'`, token); got != 0 {
		t.Fatal("outbox contains the sos link token")
	}
}
