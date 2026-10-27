package wiring

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	"birge/internal/platform/store"
	"birge/internal/platform/testinfra"
)

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
