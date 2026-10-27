package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	platformdb "birge/internal/platform/db"
	"birge/internal/platform/objectstore"
	"birge/internal/platform/testinfra"
)

// TestModerationWiringE2E covers the brief end-to-end paths: listing → queue →
// approve → active; held chat message → reject → hidden; report + block_author
// → hidden + suspended (403); verification approve → verified + outbox event.
func TestModerationWiringE2E(t *testing.T) {
	ctx := context.Background()
	url := migratedDatabase(t)
	pool, err := platformdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	authorID, reporterID := uuid.New(), uuid.New()
	tokAuthor, tokReporter := "tok-mod-author-aaaaaaaa", "tok-mod-reporter-bbbbbb"
	kratos := multiKratos(t, map[string]uuid.UUID{tokAuthor: authorID, tokReporter: reporterID})
	staffIdP := fakeStaffIdP(t)
	natsURL := testinfra.NATS(t)

	env := validEnv(t)
	env["DATABASE_URL"] = url
	env["KRATOS_PUBLIC_URL"] = kratos.URL
	env["KRATOS_ADMIN_URL"] = kratos.URL
	env["NATS_URL"] = natsURL
	env["BIRGE_DELIVERY_MODE"] = DeliveryModeDev
	env["BIRGE_HTTP_ADDR"] = "127.0.0.1:0"
	env["BIRGE_STAFF_INTROSPECTION_URL"] = staffIdP.URL + "/admin/oauth2/introspect"
	env["BIRGE_STAFF_CLIENT_ID"] = e2eStaffClientID
	env["BIRGE_STAFF_CLIENT_SECRET_FILE"] = "/keys/staff-client-secret"
	env["BIRGE_STAFF_ISSUER"] = e2eStaffIssuer
	env["BIRGE_STAFF_AUDIENCE"] = e2eStaffClientID
	env["BIRGE_STAFF_ROLES_CLAIM"] = "ext.roles"
	env["BIRGE_STAFF_MFA_AMR"] = "webauthn,hwk"
	files := validFiles(t)
	files["/keys/staff-client-secret"] = []byte(e2eStaffSecret + "\n")

	apiCfg, err := load(env, files, CommandAPI)
	if err != nil {
		t.Fatal(err)
	}
	workerCfg, err := load(env, files, CommandWorker)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := NewEnvelope(pool, apiCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	logs := &syncBuffer{}
	h, err := NewAPIHandler(ctx, APIDeps{
		Config: apiCfg, Pool: pool, Envelope: envelope,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)),
		NATS:   nc, Objects: objectstore.Disabled{},
	})
	if err != nil {
		t.Fatal(err)
	}

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunWorker(wctx, slog.New(slog.DiscardHandler), workerCfg) }()

	var districtID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM membership.districts LIMIT 1`).Scan(&districtID); err != nil {
		t.Fatal(err)
	}

	var authorMember, reporterMember uuid.UUID
	for _, tok := range []string{tokAuthor, tokReporter} {
		rec := do(t, h, http.MethodGet, "/v1/me", tok, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("me %s: %d %s", tok, rec.Code, rec.Body)
		}
		var me struct {
			ID uuid.UUID `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &me)
		name := "Author"
		if tok == tokReporter {
			name = "Reporter"
			reporterMember = me.ID
		} else {
			authorMember = me.ID
		}
		if rec := do(t, h, http.MethodPatch, "/v1/me", tok,
			fmt.Sprintf(`{"displayName":%q,"lang":"ru","districtId":%q}`, name, districtID)); rec.Code != http.StatusOK {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body)
		}
		if _, err := pool.Exec(ctx, `UPDATE membership.members SET verification = 'verified', district_id = $2 WHERE id = $1`,
			me.ID, districtID); err != nil {
			t.Fatal(err)
		}
	}

	// listing → queue → approve → active in sharing
	{
		listingBody := fmt.Sprintf(
			`{"type":"give","category":"clothes","title":"Example coat","description":"Example description for listing","districtId":%q,"acceptRules":true}`,
			districtID)
		rec := do(t, h, http.MethodPost, "/v1/listings", tokAuthor, listingBody)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create listing: %d %s", rec.Code, rec.Body)
		}
		var listing struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
			t.Fatal(err)
		}

		var itemID uuid.UUID
		waitFor(t, done, 20*time.Second, func() bool {
			err := pool.QueryRow(ctx, `SELECT id FROM moderation.items WHERE kind = 'listing' AND object_id = $1 AND status = 'open'`,
				listing.ID).Scan(&itemID)
			return err == nil && itemID != uuid.Nil
		})

		rec = do(t, h, http.MethodGet, "/v1/staff/moderation/queue?kind=listing", e2eModeratorToken, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("queue: %d %s", rec.Code, rec.Body)
		}
		var page struct {
			Items []struct {
				ID      uuid.UUID `json:"id"`
				Summary string    `json:"summary"`
			} `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, it := range page.Items {
			if it.ID == itemID {
				found = true
				if it.Summary == "" || !strings.Contains(it.Summary, "Example coat") {
					t.Fatalf("summary missing title: %q", it.Summary)
				}
			}
		}
		if !found {
			t.Fatalf("listing item not in queue: %s", rec.Body)
		}

		rec = do(t, h, http.MethodPost, "/v1/staff/moderation/items/"+itemID.String()+"/decision",
			e2eModeratorToken, `{"approve":true}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("decide: %d %s", rec.Code, rec.Body)
		}
		waitFor(t, done, 20*time.Second, func() bool {
			var status string
			err := pool.QueryRow(ctx, `SELECT status FROM sharing.listings WHERE id = $1`, listing.ID).Scan(&status)
			return err == nil && status == "active"
		})
		rec = do(t, h, http.MethodGet, "/v1/listings", tokReporter, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), listing.ID.String()) {
			t.Fatalf("feed after approve: %d %s", rec.Code, rec.Body)
		}
	}

	// held chat message → reject → hidden
	{
		rec := do(t, h, http.MethodGet, "/v1/chat/rooms?joined=false", tokAuthor, "")
		var rooms []struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rooms); err != nil || len(rooms) == 0 {
			t.Fatalf("rooms: %s", rec.Body)
		}
		roomID := rooms[0].ID
		if rec := do(t, h, http.MethodPost, "/v1/chat/rooms/"+roomID.String()+"/join", tokAuthor, ""); rec.Code != http.StatusOK {
			t.Fatalf("join author: %d %s", rec.Code, rec.Body)
		}
		if rec := do(t, h, http.MethodPost, "/v1/chat/rooms/"+roomID.String()+"/join", tokReporter, ""); rec.Code != http.StatusOK {
			t.Fatalf("join reporter: %d %s", rec.Code, rec.Body)
		}

		clientID := uuid.Must(uuid.NewV7())
		body := fmt.Sprintf(`{"clientId":%q,"text":"see https://example.invalid/scam"}`, clientID)
		rec = do(t, h, http.MethodPost, "/v1/chat/rooms/"+roomID.String()+"/messages", tokAuthor, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("send held: %d %s", rec.Code, rec.Body)
		}
		var msg struct {
			ID     uuid.UUID `json:"id"`
			Status string    `json:"status"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil || msg.Status != "held" {
			t.Fatalf("expected held: %s", rec.Body)
		}

		var itemID uuid.UUID
		waitFor(t, done, 20*time.Second, func() bool {
			err := pool.QueryRow(ctx, `SELECT id FROM moderation.items WHERE kind = 'chat_message' AND object_id = $1 AND status = 'open'`,
				msg.ID).Scan(&itemID)
			return err == nil
		})
		rec = do(t, h, http.MethodPost, "/v1/staff/moderation/items/"+itemID.String()+"/decision",
			e2eModeratorToken, `{"approve":false,"reason":"spam"}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("reject held: %d %s", rec.Code, rec.Body)
		}
		waitFor(t, done, 20*time.Second, func() bool {
			var status string
			err := pool.QueryRow(ctx, `SELECT status FROM chat.messages WHERE id = $1`, msg.ID).Scan(&status)
			return err == nil && status == "hidden"
		})
	}

	// report → reject with block_author → hidden + suspended
	{
		rec := do(t, h, http.MethodGet, "/v1/chat/rooms?joined=false", tokAuthor, "")
		var rooms []struct {
			ID uuid.UUID `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &rooms)
		roomID := rooms[0].ID
		_ = do(t, h, http.MethodPost, "/v1/chat/rooms/"+roomID.String()+"/join", tokAuthor, "")
		_ = do(t, h, http.MethodPost, "/v1/chat/rooms/"+roomID.String()+"/join", tokReporter, "")

		// Visible message: author already past newcomer holds from prior steps;
		// send plain text without link/phone.
		clientID := uuid.Must(uuid.NewV7())
		body := fmt.Sprintf(`{"clientId":%q,"text":"plain peer message for report"}`, clientID)
		rec = do(t, h, http.MethodPost, "/v1/chat/rooms/"+roomID.String()+"/messages", tokAuthor, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("send visible: %d %s", rec.Code, rec.Body)
		}
		var msg struct {
			ID     uuid.UUID `json:"id"`
			Status string    `json:"status"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &msg)
		if msg.Status != "visible" {
			// Newcomer hold may still apply; force visible for the report path.
			if _, err := pool.Exec(ctx, `UPDATE chat.messages SET status = 'visible', hold_reason = NULL WHERE id = $1`, msg.ID); err != nil {
				t.Fatal(err)
			}
			msg.Status = "visible"
		}

		rec = do(t, h, http.MethodPost, "/v1/chat/messages/"+msg.ID.String()+"/report",
			tokReporter, `{"reason":"harassment"}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("report: %d %s", rec.Code, rec.Body)
		}

		var itemID uuid.UUID
		waitFor(t, done, 20*time.Second, func() bool {
			err := pool.QueryRow(ctx, `SELECT id FROM moderation.items WHERE kind = 'report' AND object_id = $1 AND status = 'open'`,
				msg.ID).Scan(&itemID)
			return err == nil
		})
		rec = do(t, h, http.MethodPost, "/v1/staff/moderation/items/"+itemID.String()+"/decision",
			e2eModeratorToken, `{"approve":false,"reason":"unsafe","blockAuthor":true}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("reject report: %d %s", rec.Code, rec.Body)
		}
		waitFor(t, done, 20*time.Second, func() bool {
			var status string
			var suspended bool
			err1 := pool.QueryRow(ctx, `SELECT status FROM chat.messages WHERE id = $1`, msg.ID).Scan(&status)
			err2 := pool.QueryRow(ctx, `SELECT suspended_at IS NOT NULL FROM membership.members WHERE id = $1`, authorMember).Scan(&suspended)
			return err1 == nil && err2 == nil && status == "hidden" && suspended
		})
		if rec := do(t, h, http.MethodGet, "/v1/me", tokAuthor, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("suspended member: %d %s, want 403", rec.Code, rec.Body)
		} else if !strings.Contains(rec.Body.String(), "member_suspended") {
			t.Fatalf("want member_suspended: %s", rec.Body)
		}
		// Suspension never takes away the crisis path (non-negotiable 1).
		if rec := do(t, h, http.MethodPost, "/v1/me/help-requests", tokAuthor, `{"source":"menu"}`); rec.Code != http.StatusAccepted {
			t.Fatalf("suspended help request: %d %s, want 202", rec.Code, rec.Body)
		}
		if rec := do(t, h, http.MethodGet, "/v1/me/tea-requests", tokAuthor, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("suspended tea requests: %d, want 403", rec.Code)
		}
	}

	// verification approve → member verified + MemberVerified in outbox
	{
		// Use reporter (not suspended) for a fresh verification request: clear verified.
		if _, err := pool.Exec(ctx, `UPDATE membership.members SET verification = 'new' WHERE id = $1`, reporterMember); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/me/verification-requests", nil)
		req.Header.Set("X-App-Version", "1.2.0")
		req.Header.Set("X-App-Platform", "android")
		req.Header.Set("Authorization", "Bearer "+tokReporter)
		req.Header.Set("Idempotency-Key", uuid.NewString())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request verification: %d %s", rec.Code, rec.Body)
		}
		var reqID uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM membership.verification_requests WHERE member_id = $1 AND status = 'pending'`,
			reporterMember).Scan(&reqID); err != nil {
			t.Fatal(err)
		}

		var itemID uuid.UUID
		waitFor(t, done, 20*time.Second, func() bool {
			err := pool.QueryRow(ctx, `SELECT id FROM moderation.items WHERE kind = 'verification' AND object_id = $1 AND status = 'open'`,
				reqID).Scan(&itemID)
			return err == nil
		})
		rec = do(t, h, http.MethodPost, "/v1/staff/moderation/items/"+itemID.String()+"/decision",
			e2eModeratorToken, `{"approve":true}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("approve verification: %d %s", rec.Code, rec.Body)
		}
		waitFor(t, done, 20*time.Second, func() bool {
			var v string
			err := pool.QueryRow(ctx, `SELECT verification FROM membership.members WHERE id = $1`, reporterMember).Scan(&v)
			return err == nil && v == "verified"
		})
		waitFor(t, done, 10*time.Second, func() bool {
			var n int
			err := pool.QueryRow(ctx, `SELECT count(*) FROM platform.outbox WHERE event_type = 'membership.MemberVerified' AND payload->>'member_id' = $1`,
				reporterMember.String()).Scan(&n)
			return err == nil && n >= 1
		})
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("worker did not stop")
	}
}
