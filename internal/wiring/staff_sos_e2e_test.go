package wiring

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	emergencymodel "birge/internal/domains/emergency/model"
)

// TestStaffSosRoutesBehindStaffAuth proves that the moderators' SOS queue
// (/v1/staff/sos, emergency) is mounted and sits behind staffauth like
// /v1/staff/escalations: a member session is unknown to the IdP (401), a
// staff token opens it, the service refuses a psychologist (403), and with
// no provider configured it is the 503 of staffauth.Unconfigured, never a
// mux 404. The member's SOS raised over HTTP shows up, "Seen" and "Close"
// work and are audited, and the text is never logged.
func TestStaffSosRoutesBehindStaffAuth(t *testing.T) {
	e := newE2E(t)
	h := e.handler(e.cfg)

	const text = "example sos text for the staff queue"
	rec := do(t, h, http.MethodPost, "/v1/me/sos", e2eToken, `{"type":"childcare","text":"`+text+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("raise: %d %s", rec.Code, rec.Body)
	}
	var sos struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sos); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		token string
		want  int
	}{
		"no token":     {"", http.StatusUnauthorized},
		"member token": {e2eToken, http.StatusUnauthorized},
		"psychologist": {e2ePsychologistToken, http.StatusForbidden},
	} {
		for _, r := range []struct{ method, path string }{
			{http.MethodGet, "/v1/staff/sos"},
			{http.MethodPost, "/v1/staff/sos/" + sos.ID.String() + "/seen"},
			{http.MethodPost, "/v1/staff/sos/" + sos.ID.String() + "/close"},
		} {
			if rec := do(t, h, r.method, r.path, tc.token, ""); rec.Code != tc.want {
				t.Errorf("%s %s %s: %d %s, want %d", name, r.method, r.path, rec.Code, rec.Body, tc.want)
			}
		}
	}

	rec = do(t, h, http.MethodGet, "/v1/staff/sos", e2eModeratorToken, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(rec.Body.String(), sos.ID.String()) || !strings.Contains(rec.Body.String(), text) {
		t.Fatalf("moderator list: %d %q %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
	}
	if rec := do(t, h, http.MethodPost, "/v1/staff/sos/"+sos.ID.String()+"/seen", e2eModeratorToken, ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"seenBy"`) {
		t.Fatalf("seen: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodPost, "/v1/staff/sos/"+sos.ID.String()+"/close", e2eAdminToken, ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"status":"closed"`) {
		t.Fatalf("close: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodPost, "/v1/staff/sos/"+uuid.NewString()+"/seen", e2eModeratorToken, ""); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("unknown sos: %d %s, want the handler's 404 problem", rec.Code, rec.Body)
	}
	if got := countRows(t, e, `SELECT count(*) FROM platform.audit_log WHERE object_id = $1 AND action = ANY($2)`, sos.ID,
		[]string{emergencymodel.AuditViewStaffSos, emergencymodel.AuditSosSeen, emergencymodel.AuditSosCloseStaff}); got != 3 {
		t.Fatalf("staff audit rows = %d, want 3", got)
	}
	if got := countRows(t, e, `SELECT count(*) FROM emergency.sos_requests WHERE id = $1 AND status = 'closed' AND closed_by IS NOT NULL AND seen_by IS NOT NULL`, sos.ID); got != 1 {
		t.Fatal("seen and staff close not stored")
	}
	if strings.Contains(e.logs.String(), text) {
		t.Fatal("logs contain the SOS text")
	}

	// No provider: the staffauth 503, also for a member session.
	cfg := e.cfg
	cfg.StaffAuth = nil
	closed := e.handler(cfg)
	for _, token := range []string{"", e2eToken} {
		if rec := do(t, closed, http.MethodGet, "/v1/staff/sos", token, ""); rec.Code != http.StatusServiceUnavailable ||
			!strings.Contains(rec.Body.String(), "staff_auth_not_configured") {
			t.Fatalf("unconfigured, token %q: %d %s", token, rec.Code, rec.Body)
		}
	}
}
