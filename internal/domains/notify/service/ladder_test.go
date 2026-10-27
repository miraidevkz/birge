package service_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/notifytest"
	"birge/internal/domains/notify/service"
	"birge/internal/domains/notify/sms/guard"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func newLadderService(t *testing.T, clk *fixedClock) (*service.Service, *notifytest.MemoryStore, *notifytest.FakeSMS, *notifytest.FakePush, uuid.UUID) {
	t.Helper()
	member := uuid.Must(uuid.NewV7())
	store := notifytest.NewMemoryStore()
	sms := &notifytest.FakeSMS{}
	push := &notifytest.FakePush{}
	g := guard.New(guard.Config{
		AllowedPrefixes: []string{"+7"},
		PerNumberLimit:  10,
		GlobalLimit:     100,
		DailyBudget:     100,
	})
	svc := &service.Service{
		Store: store,
		Recipients: &notifytest.FakeRecipients{
			ByMember: map[uuid.UUID]model.ResolvedRecipient{
				member: {
					Kind:      model.RecipientKindMember,
					Ref:       member,
					PhoneE164: "+77001112233",
					Language:  "ru",
				},
			},
		},
		Push:   push,
		SMS:    sms,
		Guard:  g,
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: service.Config{
			CriticalSMSAfter: 60 * time.Second,
			SMSTexts: map[string]map[string]string{
				"emergency.sos": {"ru": "Birge: otkroite prilozhenie", "kk": "Birge: qosymshany ashynyz"},
			},
		},
	}
	return svc, store, sms, push, member
}

func fireDue(t *testing.T, svc *service.Service, store *notifytest.MemoryStore, kind string, now time.Time) {
	t.Helper()
	for _, id := range store.DueTimers(kind, now) {
		store.FireTimer(kind, id)
		var err error
		switch kind {
		case model.TimerPush:
			err = svc.HandlePush(context.Background(), id)
		case model.TimerExternalSMS:
			err = svc.HandleExternalSMS(context.Background(), id)
		case model.TimerCriticalSMS:
			err = svc.HandleCriticalSMS(context.Background(), id)
		default:
			t.Fatalf("unknown timer kind %q", kind)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCriticalSMSSentOnceWhenNotAcknowledged(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	svc, store, sms, _, member := newLadderService(t, clk)

	eventID := uuid.Must(uuid.NewV7())
	reqID := uuid.Must(uuid.NewV7())
	req := model.DeliveryRequest{
		RequestID: reqID,
		Recipient: model.Recipient{MemberID: &member},
		Class:     model.ClassCritical,
		Template:  "emergency.sos",
		Params:    map[string]string{},
	}
	if err := svc.HandleDeliveryRequested(context.Background(), eventID, req); err != nil {
		t.Fatal(err)
	}
	fireDue(t, svc, store, model.TimerPush, clk.t)

	n, ok := store.NotificationByRequest(reqID, model.RecipientKindMember, member)
	if !ok {
		t.Fatal("notification missing")
	}

	clk.t = start.Add(61 * time.Second)
	fireDue(t, svc, store, model.TimerCriticalSMS, clk.t)
	if sms.Len() != 1 {
		t.Fatalf("sms sends = %d, want 1", sms.Len())
	}

	if err := svc.HandleCriticalSMS(context.Background(), n.ID); err != nil {
		t.Fatal(err)
	}
	if sms.Len() != 1 {
		t.Fatalf("sms sends after replay = %d, want 1", sms.Len())
	}
}

func TestCriticalSMSNotSentAfterMarkRead(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	svc, store, sms, _, member := newLadderService(t, clk)

	eventID := uuid.Must(uuid.NewV7())
	reqID := uuid.Must(uuid.NewV7())
	if err := svc.HandleDeliveryRequested(context.Background(), eventID, model.DeliveryRequest{
		RequestID: reqID,
		Recipient: model.Recipient{MemberID: &member},
		Class:     model.ClassCritical,
		Template:  "emergency.sos",
	}); err != nil {
		t.Fatal(err)
	}
	n, ok := store.NotificationByRequest(reqID, model.RecipientKindMember, member)
	if !ok {
		t.Fatal("notification missing")
	}
	if err := svc.MarkRead(context.Background(), member, n.ID); err != nil {
		t.Fatal(err)
	}
	if due := store.DueCriticalSMS(start.Add(time.Hour)); len(due) != 0 {
		t.Fatalf("timer still scheduled after markRead: %v", due)
	}

	clk.t = start.Add(61 * time.Second)
	if err := svc.HandleCriticalSMS(context.Background(), n.ID); err != nil {
		t.Fatal(err)
	}
	if sms.Len() != 0 {
		t.Fatalf("sms sends = %d, want 0 after markRead", sms.Len())
	}
}

func TestDeliveryRequestedDedupByEventID(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc, store, _, _, member := newLadderService(t, clk)
	eventID := uuid.Must(uuid.NewV7())
	req := model.DeliveryRequest{
		RequestID: uuid.Must(uuid.NewV7()),
		Recipient: model.Recipient{MemberID: &member},
		Class:     model.ClassNormal,
		Template:  "emergency.sos",
	}
	ctx := context.Background()
	if err := svc.HandleDeliveryRequested(ctx, eventID, req); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleDeliveryRequested(ctx, eventID, req); err != nil {
		t.Fatal(err)
	}
	if store.NotificationCount() != 1 {
		t.Fatalf("notifications = %d, want 1", store.NotificationCount())
	}
}

func TestExternalRecipientSendsSMSImmediatelyNoInbox(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	store := notifytest.NewMemoryStore()
	sms := &notifytest.FakeSMS{}
	push := &notifytest.FakePush{}
	contactID := uuid.Must(uuid.NewV7())
	svc := &service.Service{
		Store: store,
		Recipients: &notifytest.FakeRecipients{
			ByTrustedContact: map[uuid.UUID]model.ResolvedRecipient{
				contactID: {
					Kind:      model.RecipientKindExternal,
					Ref:       contactID,
					PhoneE164: "+77005556677",
					Language:  "ru",
				},
			},
		},
		Push:   push,
		SMS:    sms,
		Guard:  guard.New(guard.Config{AllowedPrefixes: []string{"+7"}, PerNumberLimit: 10, GlobalLimit: 100, DailyBudget: 100}),
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: service.Config{
			SMSTexts: map[string]map[string]string{"emergency.sos": {"ru": "Birge: otkroite prilozhenie"}},
		},
	}

	reqID := uuid.Must(uuid.NewV7())
	if err := svc.HandleDeliveryRequested(context.Background(), uuid.Must(uuid.NewV7()), model.DeliveryRequest{
		RequestID: reqID,
		Recipient: model.Recipient{TrustedContactID: &contactID},
		Class:     model.ClassNormal,
		Template:  "emergency.sos",
	}); err != nil {
		t.Fatal(err)
	}
	n, ok := store.NotificationByRequest(reqID, model.RecipientKindExternal, contactID)
	if !ok {
		t.Fatal("external notification missing")
	}
	page, err := svc.ListInbox(context.Background(), contactID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("external must not appear in inbox, got %d", len(page.Items))
	}
	if due := store.DueTimers(model.TimerPush, start); len(due) != 0 {
		t.Fatalf("push timer scheduled for external: %v", due)
	}

	fireDue(t, svc, store, model.TimerExternalSMS, start)
	if sms.Len() != 1 {
		t.Fatalf("sms sends = %d, want 1", sms.Len())
	}
	if push.Len() != 0 {
		t.Fatalf("push sends = %d, want 0", push.Len())
	}
	if status, ok := store.DeliveryStatus(n.ID, model.ChannelSMS); !ok || status != model.StatusSent {
		t.Fatalf("sms status = %q ok=%v", status, ok)
	}
}

func TestCriticalSMSGuardRejectionMarksFailed(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	member := uuid.Must(uuid.NewV7())
	store := notifytest.NewMemoryStore()
	sms := &notifytest.FakeSMS{}
	svc := &service.Service{
		Store: store,
		Recipients: &notifytest.FakeRecipients{
			ByMember: map[uuid.UUID]model.ResolvedRecipient{
				member: {Kind: model.RecipientKindMember, Ref: member, PhoneE164: "+77001112233", Language: "ru"},
			},
		},
		Push: &notifytest.FakePush{},
		SMS:  sms,
		// Prefix allow-list rejects this number.
		Guard:  guard.New(guard.Config{AllowedPrefixes: []string{"+7709"}, PerNumberLimit: 10, GlobalLimit: 100, DailyBudget: 1}),
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: service.Config{
			CriticalSMSAfter: time.Second,
			SMSTexts:         map[string]map[string]string{"emergency.sos": {"ru": "Birge: otkroite prilozhenie"}},
		},
	}
	reqID := uuid.Must(uuid.NewV7())
	if err := svc.HandleDeliveryRequested(context.Background(), uuid.Must(uuid.NewV7()), model.DeliveryRequest{
		RequestID: reqID,
		Recipient: model.Recipient{MemberID: &member},
		Class:     model.ClassCritical,
		Template:  "emergency.sos",
	}); err != nil {
		t.Fatal(err)
	}
	n, ok := store.NotificationByRequest(reqID, model.RecipientKindMember, member)
	if !ok {
		t.Fatal("missing notification")
	}
	clk.t = start.Add(2 * time.Second)
	fireDue(t, svc, store, model.TimerCriticalSMS, clk.t)
	if sms.Len() != 0 {
		t.Fatalf("sms sends = %d, want 0", sms.Len())
	}
	status, ok := store.DeliveryStatus(n.ID, model.ChannelSMS)
	if !ok || status != model.StatusFailed {
		t.Fatalf("sms status = %q ok=%v, want failed", status, ok)
	}
}

func TestCriticalSMSIgnoresDailyBudget(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	member := uuid.Must(uuid.NewV7())
	store := notifytest.NewMemoryStore()
	sms := &notifytest.FakeSMS{}
	g := guard.New(guard.Config{
		AllowedPrefixes: []string{"+7"},
		PerNumberLimit:  10,
		GlobalLimit:     100,
		DailyBudget:     1,
	})
	// Exhaust daily budget with a non-critical allow.
	if err := g.Allow("+77009998877", start); err != nil {
		t.Fatal(err)
	}
	svc := &service.Service{
		Store: store,
		Recipients: &notifytest.FakeRecipients{
			ByMember: map[uuid.UUID]model.ResolvedRecipient{
				member: {Kind: model.RecipientKindMember, Ref: member, PhoneE164: "+77001112233", Language: "ru"},
			},
		},
		Push:   &notifytest.FakePush{},
		SMS:    sms,
		Guard:  g,
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: service.Config{
			CriticalSMSAfter: time.Second,
			SMSTexts:         map[string]map[string]string{"emergency.sos": {"ru": "Birge: otkroite prilozhenie"}},
		},
	}
	reqID := uuid.Must(uuid.NewV7())
	if err := svc.HandleDeliveryRequested(context.Background(), uuid.Must(uuid.NewV7()), model.DeliveryRequest{
		RequestID: reqID,
		Recipient: model.Recipient{MemberID: &member},
		Class:     model.ClassCritical,
		Template:  "emergency.sos",
	}); err != nil {
		t.Fatal(err)
	}
	clk.t = start.Add(2 * time.Second)
	fireDue(t, svc, store, model.TimerCriticalSMS, clk.t)
	if sms.Len() != 1 {
		t.Fatalf("sms sends = %d, want 1 despite exhausted daily budget", sms.Len())
	}
}
