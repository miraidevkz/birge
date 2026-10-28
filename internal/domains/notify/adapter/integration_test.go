package adapter_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/notify/adapter"
	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/notifytest"
	"birge/internal/domains/notify/port"
	"birge/internal/domains/notify/service"
	"birge/internal/domains/notify/sms/guard"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func TestDeliveryRequestedTwiceCreatesOneNotification(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	store := &adapter.PostgresStore{Pool: pool}
	member := uuid.Must(uuid.NewV7())
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := &service.Service{
		Store:      store,
		Recipients: &notifytest.FakeRecipients{},
		Push:       &notifytest.FakePush{},
		Clock:      clk,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	nc, js, err := adapter.ConnectJS(testinfra.NATS(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	if err := adapter.EnsureStream(ctx, js); err != nil {
		t.Fatal(err)
	}

	consumer := &adapter.DeliveryConsumer{JS: js, Service: svc, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	cons, err := consumer.EnsureConsumer(ctx)
	if err != nil {
		t.Fatal(err)
	}

	eventID := uuid.Must(uuid.NewV7())
	reqID := uuid.Must(uuid.NewV7())
	payload := map[string]any{
		"request_id": reqID.String(),
		"recipient":  map[string]string{"member_id": member.String()},
		"class":      "normal",
		"template":   "system.ping",
		"params":     map[string]string{},
	}
	if err := adapter.PublishDeliveryRequestedTest(ctx, js, eventID, payload); err != nil {
		t.Fatal(err)
	}
	msg, err := waitMsg(ctx, cons)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.HandleMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	_ = msg.Ack()
	if err := consumer.HandleMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notify.notifications WHERE request_id = $1`, reqID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("notifications = %d, want 1", count)
	}
}

func TestCriticalLadderWithPostgresTimer(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	store := &adapter.PostgresStore{Pool: pool}
	member := uuid.Must(uuid.NewV7())
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	sms := &notifytest.FakeSMS{}
	handlers := &adapter.TimerHandlers{Service: nil}
	svc := &service.Service{
		Store: store,
		Recipients: &notifytest.FakeRecipients{
			ByMember: map[uuid.UUID]model.ResolvedRecipient{
				member: {
					Kind:     model.RecipientKindMember,
					Ref:      member,
					Language: "ru",
				},
			},
			PhoneByMember: map[uuid.UUID]string{member: "+77001112233"},
		},
		Push:   &notifytest.FakePush{},
		SMS:    sms,
		Guard:  guard.New(guard.Config{AllowedPrefixes: []string{"+7"}, PerNumberLimit: 5, GlobalLimit: 50, DailyBudget: 50}),
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: service.Config{
			CriticalSMSAfter: time.Minute,
			SMSTexts:         map[string]map[string]string{"emergency.sos": {"ru": "Birge: otkroite prilozhenie"}},
		},
	}
	handlers.Service = svc

	eventID := uuid.Must(uuid.NewV7())
	reqID := uuid.Must(uuid.NewV7())
	if err := svc.HandleDeliveryRequested(ctx, eventID, model.DeliveryRequest{
		RequestID: reqID,
		Recipient: model.Recipient{MemberID: &member},
		Class:     model.ClassCritical,
		Template:  "emergency.sos",
	}); err != nil {
		t.Fatal(err)
	}

	var notificationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM notify.notifications WHERE request_id = $1`, reqID).Scan(&notificationID); err != nil {
		t.Fatal(err)
	}

	worker := timers.Worker{
		Pool:     pool,
		Clock:    clk,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: handlers.Handlers(),
	}
	// Push timer is due now.
	if fired, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	} else if !fired {
		t.Fatal("expected push timer to fire")
	}

	// Acknowledge before the critical SMS timer fires: SMS must not send.
	if err := svc.MarkRead(ctx, member, notificationID); err != nil {
		t.Fatal(err)
	}
	clk.t = start.Add(2 * time.Minute)
	if fired, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	} else if fired {
		t.Fatal("cancelled timer must not fire")
	}
	if sms.Len() != 0 {
		t.Fatalf("sms after ack = %d, want 0", sms.Len())
	}

	// Fresh critical notification without ack.
	clk.t = start.Add(3 * time.Minute)
	eventID2 := uuid.Must(uuid.NewV7())
	reqID2 := uuid.Must(uuid.NewV7())
	if err := svc.HandleDeliveryRequested(ctx, eventID2, model.DeliveryRequest{
		RequestID: reqID2,
		Recipient: model.Recipient{MemberID: &member},
		Class:     model.ClassCritical,
		Template:  "emergency.sos",
	}); err != nil {
		t.Fatal(err)
	}
	var n2 uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM notify.notifications WHERE request_id = $1`, reqID2).Scan(&n2); err != nil {
		t.Fatal(err)
	}
	// Fire push for the second notification.
	if fired, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	} else if !fired {
		t.Fatal("expected push timer for second notification")
	}
	clk.t = start.Add(5 * time.Minute)
	fired, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("expected critical SMS timer to fire")
	}
	if sms.Len() != 1 {
		t.Fatalf("sms sends = %d, want 1", sms.Len())
	}
	if err := svc.HandleCriticalSMS(ctx, n2); err != nil {
		t.Fatal(err)
	}
	if sms.Len() != 1 {
		t.Fatalf("sms after replay = %d, want 1", sms.Len())
	}
}

func TestExternalSMSViaTimer(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	store := &adapter.PostgresStore{Pool: pool}
	contactID := uuid.Must(uuid.NewV7())
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	sms := &notifytest.FakeSMS{}
	svc := &service.Service{
		Store: store,
		Recipients: &notifytest.FakeRecipients{
			ByTrustedContact: map[uuid.UUID]model.ResolvedRecipient{
				contactID: {
					Kind:     model.RecipientKindExternal,
					Ref:      contactID,
					Language: "ru",
				},
			},
			PhoneByExternal: map[uuid.UUID]string{contactID: "+77005556677"},
		},
		Push:   &notifytest.FakePush{},
		SMS:    sms,
		Guard:  guard.New(guard.Config{AllowedPrefixes: []string{"+7"}, PerNumberLimit: 5, GlobalLimit: 50, DailyBudget: 50}),
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: service.Config{
			SMSTexts: map[string]map[string]string{"emergency.sos": {"ru": "Birge: otkroite prilozhenie"}},
		},
	}
	handlers := &adapter.TimerHandlers{Service: svc}

	reqID := uuid.Must(uuid.NewV7())
	if err := svc.HandleDeliveryRequested(ctx, uuid.Must(uuid.NewV7()), model.DeliveryRequest{
		RequestID: reqID,
		Recipient: model.Recipient{TrustedContactID: &contactID},
		Class:     model.ClassNormal,
		Template:  "emergency.sos",
	}); err != nil {
		t.Fatal(err)
	}

	var kind string
	if err := pool.QueryRow(ctx, `SELECT recipient_kind FROM notify.notifications WHERE request_id = $1`, reqID).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != model.RecipientKindExternal {
		t.Fatalf("kind = %q, want external", kind)
	}

	worker := timers.Worker{
		Pool:     pool,
		Clock:    clk,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: handlers.Handlers(),
	}
	if fired, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	} else if !fired {
		t.Fatal("expected ExternalSMS timer")
	}
	if sms.Len() != 1 {
		t.Fatalf("sms sends = %d, want 1", sms.Len())
	}
}

func waitMsg(ctx context.Context, cons jetstream.Consumer) (jetstream.Msg, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(200*time.Millisecond))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		for msg := range msgs.Messages() {
			return msg, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
}

func TestDeleteRejectedDeviceMatchesToken(t *testing.T) {
	ctx := context.Background()
	store := &adapter.PostgresStore{Pool: testinfra.Postgres(t)}
	member := uuid.Must(uuid.NewV7())
	device := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	register := func(token string) {
		t.Helper()
		if err := store.InTx(ctx, func(tx port.Tx) error {
			return tx.UpsertDevice(ctx, model.Device{MemberID: member, DeviceID: device, Platform: model.PlatformAndroid, PushToken: token, UpdatedAt: now})
		}); err != nil {
			t.Fatal(err)
		}
	}
	remove := func(token string) (removed bool, left int) {
		t.Helper()
		if err := store.InTx(ctx, func(tx port.Tx) error {
			var err error
			if removed, err = tx.DeleteRejectedDevice(ctx, member, device, token); err != nil {
				return err
			}
			devices, err := tx.ListDevices(ctx, member)
			left = len(devices)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return removed, left
	}

	register("token-old-1")
	register("token-new-1") // the app re-registered before the rejection landed
	if removed, left := remove("token-old-1"); removed || left != 1 {
		t.Fatalf("stale rejection: removed=%v left=%d, want the fresh registration kept", removed, left)
	}
	if removed, left := remove("token-new-1"); !removed || left != 0 {
		t.Fatalf("rejection: removed=%v left=%d, want the registration gone", removed, left)
	}
}
