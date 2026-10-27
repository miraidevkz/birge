package wiring

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	safetyadapter "birge/internal/domains/safety/adapter"
	safetymodel "birge/internal/domains/safety/model"
	safetyservice "birge/internal/domains/safety/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	"birge/internal/platform/store"
	"birge/internal/platform/testinfra"
)

const staffDutyPhone = "+77001234567" // fictional; allowed by BIRGE_SMS_ALLOWED_PREFIXES

// TestWorkerDeliversCriticalNotification runs the real worker against
// PostgreSQL and in-process JetStream: an outbox event reaches the notify
// consumer through the relay, the inbox row appears for the member, and the
// push and the critical SMS fallback go through the log-only senders, with
// the phone looked up in the (fake) Kratos admin API and never logged.
func TestWorkerDeliversCriticalNotification(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	api := e.handler(e.cfg)

	rec := do(t, api, http.MethodGet, "/v1/me", e2eToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me: %d %s", rec.Code, rec.Body)
	}
	var memberID uuid.UUID
	if err := e.pool.QueryRow(ctx, "SELECT id FROM membership.members WHERE identity_id = $1", e.identity).Scan(&memberID); err != nil {
		t.Fatal(err)
	}

	natsURL := testinfra.NATS(t)
	env := e.env
	env["NATS_URL"] = natsURL
	env["BIRGE_HTTP_ADDR"] = "127.0.0.1:0"
	env["NOTIFY_CRITICAL_SMS_AFTER"] = "1s"
	env["BIRGE_SMS_TEXTS"] = `{"safety.escalation_opened":{"ru":"Neutral text","kk":"Neutral text"}}`
	cfg, err := load(env, e.files, CommandWorker)
	if err != nil {
		t.Fatal(err)
	}

	requestID := uuid.New()
	if err := db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
		_, err := outbox.Enqueue(ctx, store.New(tx), time.Now().UTC(), "", outbox.Event{
			Type: "notify.DeliveryRequested", Version: 1,
			Data: map[string]any{
				"request_id": requestID,
				"recipient":  map[string]any{"member_id": memberID},
				"class":      "critical",
				"template":   "safety.escalation_opened",
			},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	logs := &syncBuffer{}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunWorker(wctx, slog.New(slog.NewJSONHandler(logs, nil)), cfg) }()

	waitFor(t, done, 20*time.Second, func() bool {
		var n int
		err := e.pool.QueryRow(ctx, `SELECT count(*) FROM notify.deliveries d
			JOIN notify.notifications n ON n.id = d.notification_id
			WHERE n.recipient_ref = $1 AND d.channel = 'sms' AND d.status = 'sent'`, memberID).Scan(&n)
		return err == nil && n == 1
	})

	rec = do(t, api, http.MethodGet, "/v1/me/inbox", e2eToken, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "safety.escalation_opened") {
		t.Fatalf("inbox: %d %s", rec.Code, rec.Body)
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

	out := logs.String()
	if strings.Contains(out, testPhone) || strings.Contains(out, "Neutral text") {
		t.Errorf("worker logs contain the phone or the SMS text: %s", out)
	}
	if !strings.Contains(out, `"channel":"sms"`) {
		t.Errorf("log-only SMS sender not used: %s", out)
	}

	// The stream is configured as the outbox needs it, and EnsureStream is idempotent.
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureStream(ctx, js, defaultEventsMaxAge, defaultEventsMaxBytes); err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(ctx, StreamName)
	if err != nil {
		t.Fatal(err)
	}
	info := stream.CachedInfo()
	if info.Config.Duplicates != StreamDuplicates || info.Config.Storage != jetstream.FileStorage {
		t.Errorf("stream config: duplicates %s, storage %v", info.Config.Duplicates, info.Config.Storage)
	}
	if info.Config.MaxAge != defaultEventsMaxAge || info.Config.MaxBytes != defaultEventsMaxBytes {
		t.Errorf("stream retention: MaxAge %s, MaxBytes %d", info.Config.MaxAge, info.Config.MaxBytes)
	}
}

// TestHelpRequestAlertsOnDutyStaff: a member help request opens one escalation
// and a notify.DeliveryRequested to staff_role=psychologist; the worker
// delivers an SMS to the on-duty shift phone via the log-only sender
// (asserted by notification id and channel only — never the phone).
func TestHelpRequestAlertsOnDutyStaff(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	api := e.handler(e.cfg)

	rec := do(t, api, http.MethodGet, "/v1/me", e2eToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me: %d %s", rec.Code, rec.Body)
	}

	safety := &safetyservice.Service{
		Store:  &safetyadapter.PostgresStore{Pool: e.pool},
		Sealer: e.envelope,
		Clock:  clock.System{},
		Logger: slog.New(slog.DiscardHandler),
		Settings: safetyservice.Settings{
			BackupAfter:  e.cfg.EscalationBackupAfter,
			CrisisPhones: safetyCrisisPhones(e.cfg.CrisisPhones),
			StaffLang:    e.cfg.StaffSMSLang,
		},
	}
	now := time.Now().UTC()
	shiftID := uuid.Must(uuid.NewV7())
	if _, err := safety.AddDutyShift(ctx, auth.Principal{
		IdentityID: uuid.New(), Staff: true, Roles: []string{safetymodel.RoleAdmin},
	}, safetymodel.NewDutyShift{
		ID: shiftID, StaffID: uuid.New(), Role: safetymodel.RolePsychologist,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(8 * time.Hour), Phone: staffDutyPhone,
	}); err != nil {
		t.Fatalf("seed duty shift: %v", err)
	}

	rec = do(t, api, http.MethodPost, "/v1/me/help-requests", e2eToken, `{"source":"menu"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("help-request: %d %s", rec.Code, rec.Body)
	}
	var help struct {
		EscalationID uuid.UUID `json:"escalationId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &help); err != nil || help.EscalationID == uuid.Nil {
		t.Fatalf("help-request body: %s %v", rec.Body, err)
	}
	var escalations int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM safety.escalations WHERE id = $1 AND status = 'open'`, help.EscalationID).Scan(&escalations); err != nil || escalations != 1 {
		t.Fatalf("escalations = %d, %v", escalations, err)
	}
	var deliveryRequests int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM platform.outbox WHERE event_type = 'notify.DeliveryRequested'`).Scan(&deliveryRequests); err != nil || deliveryRequests != 1 {
		t.Fatalf("DeliveryRequested outbox rows = %d, %v", deliveryRequests, err)
	}

	natsURL := testinfra.NATS(t)
	env := e.env
	env["NATS_URL"] = natsURL
	env["BIRGE_HTTP_ADDR"] = "127.0.0.1:0"
	env["BIRGE_SMS_TEXTS"] = `{"safety.escalation_opened":{"ru":"Neutral staff alert","kk":"Neutral staff alert"}}`
	cfg, err := load(env, e.files, CommandWorker)
	if err != nil {
		t.Fatal(err)
	}

	logs := &syncBuffer{}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunWorker(wctx, slog.New(slog.NewJSONHandler(logs, nil)), cfg) }()

	var notificationID uuid.UUID
	waitFor(t, done, 20*time.Second, func() bool {
		err := e.pool.QueryRow(ctx, `SELECT n.id FROM notify.notifications n
			JOIN notify.deliveries d ON d.notification_id = n.id
			WHERE n.recipient_kind = 'staff' AND n.recipient_ref = $1
			  AND d.channel = 'sms' AND d.status = 'sent'`, shiftID).Scan(&notificationID)
		return err == nil && notificationID != uuid.Nil
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("worker did not stop")
	}

	out := logs.String()
	for _, secret := range []string{staffDutyPhone, testPhone, "Neutral staff alert", e2eDisplayName} {
		if strings.Contains(out, secret) {
			t.Errorf("worker logs contain %q", secret)
		}
	}
	if !strings.Contains(out, `"channel":"sms"`) || !strings.Contains(out, notificationID.String()) {
		t.Errorf("log-only SMS missing channel or notification id: %s", out)
	}
}

// TestCrisisCheckinOpensEscalation: a crisis-flagged check-in returns crisis
// phones and opens a safety escalation (source checkin).
func TestCrisisCheckinOpensEscalation(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	api := e.handler(e.cfg)

	if rec := do(t, api, http.MethodGet, "/v1/me", e2eToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me: %d %s", rec.Code, rec.Body)
	}

	body := `{
		"questionnaireVersion":"test-v1",
		"answers":[
			{"questionId":"phq_1","value":0},{"questionId":"phq_2","value":0},
			{"questionId":"phq_3","value":0},{"questionId":"phq_4","value":3},
			{"questionId":"pss_1","value":0},{"questionId":"pss_2","value":0},
			{"questionId":"pss_3","value":0},{"questionId":"pss_4","value":0}
		]
	}`
	rec := do(t, api, http.MethodPost, "/v1/me/checkins", e2eToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkin: %d %s", rec.Code, rec.Body)
	}
	var result struct {
		Flag         string     `json:"flag"`
		EscalationID *uuid.UUID `json:"escalationId"`
		CrisisPhones []struct {
			Number string `json:"number"`
			Label  string `json:"label"`
		} `json:"crisisPhones"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Flag != "crisis" || result.EscalationID == nil || *result.EscalationID == uuid.Nil {
		t.Fatalf("checkin result = %+v", result)
	}
	if len(result.CrisisPhones) == 0 || result.CrisisPhones[0].Number == "" {
		t.Fatalf("missing crisis phones: %+v", result.CrisisPhones)
	}
	var source string
	if err := e.pool.QueryRow(ctx, `SELECT source FROM safety.escalations WHERE id = $1`, *result.EscalationID).Scan(&source); err != nil || source != "checkin" {
		t.Fatalf("escalation source = %q, %v; want checkin", source, err)
	}

	logs := e.logs.String()
	for _, secret := range []string{result.CrisisPhones[0].Number, `"value":3`, "phq_4"} {
		if strings.Contains(logs, secret) {
			t.Errorf("API logs contain personal/answer data %q", secret)
		}
	}
}

// waitFor polls cond until it holds, failing early if the worker exits.
func waitFor(t *testing.T, done <-chan error, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		select {
		case err := <-done:
			t.Fatalf("worker exited early: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("condition not met before the deadline")
		}
	}
}

const (
	sosTextE2E        = "Need help with an example item now"
	contactNameE2E    = "Example Contact"
	contactPhoneE2E   = "+77001110001"
	moderatorPhoneE2E = "+77001234567"
)

// TestSOSRaiseAlertsModeratorAndExternalContact: raising an SOS reaches the
// on-duty moderator and an external trusted contact by SMS (log-only sender;
// assert notification id and channel only — never the phone, name or SOS text).
func TestSOSRaiseAlertsModeratorAndExternalContact(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	api := e.handler(e.cfg)

	if rec := do(t, api, http.MethodGet, "/v1/me", e2eToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me: %d %s", rec.Code, rec.Body)
	}
	rec := do(t, api, http.MethodPost, "/v1/me/trusted-contacts", e2eToken,
		`{"kind":"external","name":"`+contactNameE2E+`","phone":"`+contactPhoneE2E+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add contact: %d %s", rec.Code, rec.Body)
	}
	var contact struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &contact); err != nil || contact.ID == uuid.Nil {
		t.Fatalf("contact body: %s %v", rec.Body, err)
	}

	safety := &safetyservice.Service{
		Store:  &safetyadapter.PostgresStore{Pool: e.pool},
		Sealer: e.envelope,
		Clock:  clock.System{},
		Logger: slog.New(slog.DiscardHandler),
		Settings: safetyservice.Settings{
			BackupAfter:  e.cfg.EscalationBackupAfter,
			CrisisPhones: safetyCrisisPhones(e.cfg.CrisisPhones),
			StaffLang:    e.cfg.StaffSMSLang,
		},
	}
	now := time.Now().UTC()
	shiftID := uuid.Must(uuid.NewV7())
	if _, err := safety.AddDutyShift(ctx, auth.Principal{
		IdentityID: uuid.New(), Staff: true, Roles: []string{safetymodel.RoleAdmin},
	}, safetymodel.NewDutyShift{
		ID: shiftID, StaffID: uuid.New(), Role: safetymodel.RoleModerator,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(8 * time.Hour), Phone: moderatorPhoneE2E,
	}); err != nil {
		t.Fatalf("seed moderator shift: %v", err)
	}

	rec = do(t, api, http.MethodPost, "/v1/me/sos", e2eToken,
		`{"type":"item_or_medicine","text":"`+sosTextE2E+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("raise sos: %d %s", rec.Code, rec.Body)
	}

	natsURL := testinfra.NATS(t)
	env := e.env
	env["NATS_URL"] = natsURL
	env["BIRGE_HTTP_ADDR"] = "127.0.0.1:0"
	env["BIRGE_SMS_TEXTS"] = `{"emergency.sos_raised":{"ru":"Neutral moderator alert","kk":"Neutral moderator alert"},` +
		`"emergency.sos_raised_contact":{"ru":"Neutral contact alert","kk":"Neutral contact alert"}}`
	cfg, err := load(env, e.files, CommandWorker)
	if err != nil {
		t.Fatal(err)
	}

	logs := &syncBuffer{}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunWorker(wctx, slog.New(slog.NewJSONHandler(logs, nil)), cfg) }()

	var staffNotif, extNotif uuid.UUID
	waitFor(t, done, 25*time.Second, func() bool {
		_ = e.pool.QueryRow(ctx, `SELECT n.id FROM notify.notifications n
			JOIN notify.deliveries d ON d.notification_id = n.id
			WHERE n.recipient_kind = 'staff' AND n.recipient_ref = $1
			  AND n.template = 'emergency.sos_raised'
			  AND d.channel = 'sms' AND d.status = 'sent'`, shiftID).Scan(&staffNotif)
		_ = e.pool.QueryRow(ctx, `SELECT n.id FROM notify.notifications n
			JOIN notify.deliveries d ON d.notification_id = n.id
			WHERE n.recipient_kind = 'external' AND n.recipient_ref = $1
			  AND n.template = 'emergency.sos_raised_contact'
			  AND d.channel = 'sms' AND d.status = 'sent'`, contact.ID).Scan(&extNotif)
		return staffNotif != uuid.Nil && extNotif != uuid.Nil
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("worker did not stop")
	}

	out := logs.String()
	for _, secret := range []string{
		sosTextE2E, contactNameE2E, contactPhoneE2E, moderatorPhoneE2E, testPhone,
		"Neutral moderator alert", "Neutral contact alert", e2eDisplayName,
	} {
		if strings.Contains(out, secret) {
			t.Errorf("worker logs contain %q", secret)
		}
	}
	if !strings.Contains(out, `"channel":"sms"`) ||
		!strings.Contains(out, staffNotif.String()) ||
		!strings.Contains(out, extNotif.String()) {
		t.Errorf("log-only SMS missing channel or notification ids: %s", out)
	}
}

// TestSOSShareDistrictPush: sharing an SOS requests a delivery for each
// verified member of the district except the author and anyone blocked with her.
func TestSOSShareDistrictPush(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	api := e.handler(e.cfg)

	rec := do(t, api, http.MethodGet, "/v1/me", e2eToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me: %d %s", rec.Code, rec.Body)
	}
	var author struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &author); err != nil || author.ID == uuid.Nil {
		t.Fatalf("me body: %s %v", rec.Body, err)
	}

	var districtID uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM membership.districts WHERE code = 'demo_district_a'`).Scan(&districtID); err != nil {
		t.Fatalf("demo district: %v", err)
	}
	rec = do(t, api, http.MethodPatch, "/v1/me", e2eToken, `{"districtId":"`+districtID.String()+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set district: %d %s", rec.Code, rec.Body)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE membership.members SET verification = 'verified' WHERE id = $1`, author.ID); err != nil {
		t.Fatal(err)
	}

	neighborOK := uuid.Must(uuid.NewV7())
	neighborBlocked := uuid.Must(uuid.NewV7())
	for _, id := range []uuid.UUID{neighborOK, neighborBlocked} {
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO membership.members (id, identity_id, district_id, verification, lang, created_at, updated_at)
			VALUES ($1, $2, $3, 'verified', 'ru', now(), now())`, id, uuid.New(), districtID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO membership.blocks (blocker, blocked, since) VALUES ($1, $2, now())`,
		author.ID, neighborBlocked); err != nil {
		t.Fatal(err)
	}

	rec = do(t, api, http.MethodPost, "/v1/me/sos", e2eToken,
		`{"type":"other","text":"`+sosTextE2E+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("raise sos: %d %s", rec.Code, rec.Body)
	}
	var sos struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sos); err != nil || sos.ID == uuid.Nil {
		t.Fatalf("sos body: %s %v", rec.Body, err)
	}
	rec = do(t, api, http.MethodPost, "/v1/me/sos/"+sos.ID.String()+"/share", e2eToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("share: %d %s", rec.Code, rec.Body)
	}

	natsURL := testinfra.NATS(t)
	env := e.env
	env["NATS_URL"] = natsURL
	env["BIRGE_HTTP_ADDR"] = "127.0.0.1:0"
	cfg, err := load(env, e.files, CommandWorker)
	if err != nil {
		t.Fatal(err)
	}

	logs := &syncBuffer{}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunWorker(wctx, slog.New(slog.NewJSONHandler(logs, nil)), cfg) }()

	waitFor(t, done, 25*time.Second, func() bool {
		var n int
		err := e.pool.QueryRow(ctx, `
			SELECT count(*) FROM notify.notifications
			WHERE template = 'emergency.sos_shared'
			  AND recipient_kind = 'member' AND recipient_ref = $1`, neighborOK).Scan(&n)
		return err == nil && n == 1
	})

	var blockedCount, authorCount int
	if err := e.pool.QueryRow(ctx, `
		SELECT count(*) FROM notify.notifications
		WHERE template = 'emergency.sos_shared'
		  AND recipient_kind = 'member' AND recipient_ref = $1`, neighborBlocked).Scan(&blockedCount); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `
		SELECT count(*) FROM notify.notifications
		WHERE template = 'emergency.sos_shared'
		  AND recipient_kind = 'member' AND recipient_ref = $1`, author.ID).Scan(&authorCount); err != nil {
		t.Fatal(err)
	}
	if blockedCount != 0 || authorCount != 0 {
		t.Fatalf("blocked=%d author=%d; want 0 for both", blockedCount, authorCount)
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

	out := logs.String()
	for _, secret := range []string{sosTextE2E, e2eDisplayName, testPhone} {
		if strings.Contains(out, secret) {
			t.Errorf("worker logs contain %q", secret)
		}
	}
}
