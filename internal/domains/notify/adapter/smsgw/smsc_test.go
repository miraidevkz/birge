package smsgw_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/notify/adapter/smsgw"
	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/notifytest"
	"birge/internal/domains/notify/port"
	"birge/internal/domains/notify/service"
	"birge/internal/domains/notify/sms/guard"
)

const (
	testLogin    = "TEST-login"
	testPassword = "TEST-password-s3cret"
	testPhone    = "+77001234567"
	testText     = "TEST Birge: open the app"
)

// fakeSMSC is an httptest SMSC.kz send.php.
type fakeSMSC struct {
	t   *testing.T
	srv *httptest.Server

	mu     sync.Mutex
	status int
	reply  string
	calls  int
	form   map[string]string
}

func newFakeSMSC(t *testing.T) *fakeSMSC {
	f := &fakeSMSC{t: t, status: http.StatusOK, reply: `{"id": 4711, "cnt": 1}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		if r.Method != http.MethodPost || r.URL.Path != "/sys/send.php" {
			f.t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			f.t.Errorf("credentials or phone in the URL query: %q", r.URL.RawQuery)
		}
		if err := r.ParseForm(); err != nil {
			f.t.Errorf("form: %v", err)
		}
		f.form = map[string]string{}
		for k := range r.PostForm {
			f.form[k] = r.PostForm.Get(k)
		}
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.reply)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSMSC) set(status int, reply string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.reply = status, reply
}

func newSender(f *fakeSMSC) *smsgw.HTTPSender {
	return &smsgw.HTTPSender{
		Provider: smsgw.SMSC{BaseURL: f.srv.URL, Login: testLogin, Password: testPassword, Sender: "BIRGE"},
		Client:   f.srv.Client(),
	}
}

func testMessage() port.SMSMessage {
	return port.SMSMessage{NotificationID: uuid.Must(uuid.NewV7()), PhoneE164: testPhone, Text: testText}
}

func TestSMSCSendsForm(t *testing.T) {
	t.Parallel()
	f := newFakeSMSC(t)
	id, err := newSender(f).Send(context.Background(), testMessage())
	if err != nil {
		t.Fatal(err)
	}
	if id != "4711" {
		t.Errorf("provider id = %q, want 4711", id)
	}
	want := map[string]string{
		"login": testLogin, "psw": testPassword, "phones": "77001234567", "mes": testText,
		"sender": "BIRGE", "fmt": "3", "charset": "utf-8",
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if fmt.Sprint(f.form) != fmt.Sprint(want) {
		t.Errorf("form = %v\nwant   %v", f.form, want)
	}
}

func TestSMSCErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		reply  string
		want   error // nil = transient
	}{
		{"parameters", 200, `{"error":"parameters","error_code":1}`, port.ErrPermanent},
		{"credentials", 200, `{"error":"login","error_code":2}`, nil},
		{"balance", 200, `{"error":"balance","error_code":3,"id":5}`, nil},
		{"ip blocked", 200, `{"error":"blocked","error_code":4}`, nil},
		{"forbidden text", 200, `{"error":"forbidden","error_code":6,"id":5}`, port.ErrPermanent},
		{"bad number", 200, `{"error":"invalid phone","error_code":7,"id":5}`, port.ErrPermanent},
		{"undeliverable", 200, `{"error":"undeliverable","error_code":8,"id":5}`, port.ErrPermanent},
		{"duplicate", 200, `{"error":"duplicate","error_code":9}`, port.ErrPermanent},
		{"unknown code", 200, `{"error":"new","error_code":42}`, nil},
		{"rate limited", 429, ``, nil},
		{"server error", 503, `<html>down</html>`, nil},
		{"wrong endpoint", 404, `<html>not found</html>`, nil},
		{"not json", 200, `OK - 1 SMS`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeSMSC(t)
			f.set(tt.status, tt.reply)
			_, err := newSender(f).Send(context.Background(), testMessage())
			if err == nil {
				t.Fatal("Send succeeded")
			}
			if got := errors.Is(err, port.ErrPermanent); got != (tt.want != nil) {
				t.Errorf("errors.Is(%v, ErrPermanent) = %v", err, got)
			}
			for _, secret := range []string{testPhone, "77001234567", testPassword, testLogin, testText} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error text leaks %q: %v", secret, err)
				}
			}
		})
	}
}

func TestSMSCTransportErrorHidesNothingSensitive(t *testing.T) {
	t.Parallel()
	f := newFakeSMSC(t)
	f.srv.Close()
	_, err := newSender(f).Send(context.Background(), testMessage())
	if err == nil || errors.Is(err, port.ErrPermanent) {
		t.Fatalf("err = %v, want a transient error", err)
	}
	if strings.Contains(err.Error(), testPassword) || strings.Contains(err.Error(), "77001234567") {
		t.Errorf("error text leaks request data: %v", err)
	}
}

func TestSMSCRedactsCredentials(t *testing.T) {
	t.Parallel()
	p := smsgw.SMSC{Login: testLogin, Password: testPassword}
	if s := fmt.Sprintf("%v %+v %#v", p, p, p); strings.Contains(s, testPassword) || strings.Contains(s, testLogin) {
		t.Errorf("SMSC prints its credentials: %s", s)
	}
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// TestServiceWithSMSC runs the critical SMS path against the fake SMSC and
// checks retry versus give-up and that the log holds no phone, text or credential.
func TestServiceWithSMSC(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     int
		reply      string
		wantErr    bool
		wantStatus string
	}{
		{"delivered", 200, `{"id":1,"cnt":1}`, false, model.StatusSent},
		{"bad number gives up", 200, `{"error":"invalid phone","error_code":7}`, false, model.StatusFailed},
		{"server error retries", 503, ``, true, model.StatusFailed},
		{"out of balance retries", 200, `{"error":"balance","error_code":3}`, true, model.StatusFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeSMSC(t)
			f.set(tt.status, tt.reply)
			var logs bytes.Buffer
			start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
			clk := &fixedClock{t: start}
			store := notifytest.NewMemoryStore()
			member := uuid.Must(uuid.NewV7())
			svc := &service.Service{
				Store:      store,
				Recipients: &notifytest.FakeRecipients{PhoneByMember: map[uuid.UUID]string{member: testPhone}},
				SMS:        newSender(f),
				Guard:      guard.New(guard.Config{AllowedPrefixes: []string{"+7700"}, PerNumberLimit: 10, GlobalLimit: 10}),
				Clock:      clk,
				Logger:     slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
				Config: service.Config{
					SMSTexts: map[string]map[string]string{"emergency.sos_raised": {"ru": testText}},
				},
			}
			ctx := context.Background()
			reqID := uuid.Must(uuid.NewV7())
			if err := svc.HandleDeliveryRequested(ctx, uuid.Must(uuid.NewV7()), model.DeliveryRequest{
				RequestID: reqID,
				Recipient: model.Recipient{MemberID: &member},
				Class:     model.ClassCritical,
				Template:  "emergency.sos_raised",
			}); err != nil {
				t.Fatal(err)
			}
			n, _ := store.NotificationByRequest(reqID, model.RecipientKindMember, member)
			clk.t = start.Add(2 * time.Minute)

			err := svc.HandleCriticalSMS(ctx, n.ID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HandleCriticalSMS error = %v, want error %v", err, tt.wantErr)
			}
			if status, _ := store.DeliveryStatus(n.ID, model.ChannelSMS); status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			out := logs.String()
			for _, secret := range []string{testPhone, "77001234567", testPassword, testLogin, testText} {
				if strings.Contains(out, secret) {
					t.Errorf("log contains %q:\n%s", secret, out)
				}
			}
		})
	}
}
