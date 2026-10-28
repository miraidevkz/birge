package service_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/notifytest"
	"birge/internal/domains/notify/service"
	"birge/internal/domains/notify/sms/guard"
)

// fakeLinks returns a fixed link or error and records its calls.
type fakeLinks struct {
	link  string
	err   error
	calls int
}

func (f *fakeLinks) LinkFor(_ context.Context, template, kind string, _ uuid.UUID) (string, error) {
	f.calls++
	if template != "emergency.plan_sent" || kind != model.RecipientKindExternal {
		return "", model.ErrNotFound
	}
	return f.link, f.err
}

const secretLink = "https://example.invalid/plan#AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// TestSMSLinkPlaceholder: a text with {link} gets the link minted at send
// time (and only then); no link skips the SMS; a transient failure leaves it
// pending for a retry. The link never reaches the log.
func TestSMSLinkPlaceholder(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		links      *fakeLinks
		wantSMS    int
		wantStatus string
		wantErr    bool
	}{
		"link":      {links: &fakeLinks{link: secretLink}, wantSMS: 1, wantStatus: model.StatusSent},
		"no link":   {links: &fakeLinks{err: model.ErrNotFound}, wantStatus: model.StatusSkipped},
		"no port":   {links: nil, wantStatus: model.StatusSkipped},
		"transient": {links: &fakeLinks{err: errors.New("db down")}, wantStatus: model.StatusPending, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
			contactID := uuid.Must(uuid.NewV7())
			store := notifytest.NewMemoryStore()
			sms := &notifytest.FakeSMS{}
			logs := &bytes.Buffer{}
			svc := &service.Service{
				Store: store,
				Recipients: &notifytest.FakeRecipients{
					ByTrustedContact: map[uuid.UUID]model.ResolvedRecipient{
						contactID: {Kind: model.RecipientKindExternal, Ref: contactID, Language: "ru"},
					},
					PhoneByExternal: map[uuid.UUID]string{contactID: "+77005556677"},
				},
				Push:   &notifytest.FakePush{},
				SMS:    sms,
				Guard:  guard.New(guard.Config{AllowedPrefixes: []string{"+7"}, PerNumberLimit: 10, GlobalLimit: 100, DailyBudget: 100}),
				Clock:  &fixedClock{t: start},
				Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
				Config: service.Config{
					SMSTexts: map[string]map[string]string{"emergency.plan_sent": {"ru": "Birge: plan {link}"}},
				},
			}
			if tc.links != nil {
				svc.Links = tc.links
			}
			reqID := uuid.Must(uuid.NewV7())
			if err := svc.HandleDeliveryRequested(context.Background(), uuid.Must(uuid.NewV7()), model.DeliveryRequest{
				RequestID: reqID, Recipient: model.Recipient{TrustedContactID: &contactID},
				Class: model.ClassNormal, Template: "emergency.plan_sent",
			}); err != nil {
				t.Fatal(err)
			}
			if tc.links != nil && tc.links.calls != 0 {
				t.Fatal("link minted before the send")
			}
			n, _ := store.NotificationByRequest(reqID, model.RecipientKindExternal, contactID)
			err := svc.HandleExternalSMS(context.Background(), n.ID)
			if (err != nil) != tc.wantErr {
				t.Fatalf("send error = %v", err)
			}
			if sms.Len() != tc.wantSMS {
				t.Fatalf("sms sends = %d", sms.Len())
			}
			if tc.wantSMS == 1 && sms.Calls[0].Text != "Birge: plan "+secretLink {
				t.Fatalf("sms text = %q", sms.Calls[0].Text)
			}
			if status, _ := store.DeliveryStatus(n.ID, model.ChannelSMS); status != tc.wantStatus {
				t.Fatalf("status = %q", status)
			}
			if strings.Contains(logs.String(), secretLink) || strings.Contains(logs.String(), "#AAAA") {
				t.Fatal("logs contain the link")
			}
		})
	}
}

// downCounters is a guard counter store whose database is down.
type downCounters struct{}

func (downCounters) Hit(context.Context, string, time.Duration, int, time.Time) (bool, error) {
	return false, errors.New("database down")
}

func (downCounters) Claim(context.Context, string, time.Time, time.Time) (bool, error) {
	return false, errors.New("database down")
}

// TestSMSGuardStoreDownIsTransient: when the guard cannot count, an ordinary
// SMS is neither sent nor skipped; it stays pending and the timer retries.
func TestSMSGuardStoreDownIsTransient(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	contactID := uuid.Must(uuid.NewV7())
	store := notifytest.NewMemoryStore()
	sms := &notifytest.FakeSMS{}
	svc := &service.Service{
		Store: store,
		Recipients: &notifytest.FakeRecipients{
			ByTrustedContact: map[uuid.UUID]model.ResolvedRecipient{
				contactID: {Kind: model.RecipientKindExternal, Ref: contactID, Language: "ru"},
			},
			PhoneByExternal: map[uuid.UUID]string{contactID: "+77005556677"},
		},
		Push: &notifytest.FakePush{},
		SMS:  sms,
		Guard: guard.New(guard.Config{AllowedPrefixes: []string{"+7"}, PerNumberLimit: 10, Store: downCounters{},
			Logger: slog.New(slog.DiscardHandler)}),
		Links:  &fakeLinks{link: secretLink},
		Clock:  &fixedClock{t: start},
		Logger: slog.New(slog.DiscardHandler),
		Config: service.Config{SMSTexts: map[string]map[string]string{"emergency.plan_sent": {"ru": "Birge: plan {link}"}}},
	}
	reqID := uuid.Must(uuid.NewV7())
	if err := svc.HandleDeliveryRequested(context.Background(), uuid.Must(uuid.NewV7()), model.DeliveryRequest{
		RequestID: reqID, Recipient: model.Recipient{TrustedContactID: &contactID},
		Class: model.ClassNormal, Template: "emergency.plan_sent",
	}); err != nil {
		t.Fatal(err)
	}
	n, _ := store.NotificationByRequest(reqID, model.RecipientKindExternal, contactID)
	if err := svc.HandleExternalSMS(context.Background(), n.ID); !errors.Is(err, guard.ErrUnavailable) {
		t.Fatalf("send error = %v, want guard.ErrUnavailable for a retry", err)
	}
	if sms.Len() != 0 {
		t.Fatalf("sms sends = %d", sms.Len())
	}
	if status, _ := store.DeliveryStatus(n.ID, model.ChannelSMS); status != model.StatusPending {
		t.Fatalf("status = %q, want pending", status)
	}
}
