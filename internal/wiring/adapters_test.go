package wiring

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	membershipmodel "birge/internal/domains/membership/model"
	notifymodel "birge/internal/domains/notify/model"
	notifyport "birge/internal/domains/notify/port"
	"birge/internal/platform/timers"
)

const testPhone = "+77000000000" // fictional number in an allowed test prefix

func TestClientIP(t *testing.T) {
	tests := []struct {
		name, header string
		values       []string
		want         string
	}{
		{"remote addr by default", "", []string{"203.0.113.9"}, "192.0.2.1"},
		{"right-most value", "X-Forwarded-For", []string{"198.51.100.7, 203.0.113.9"}, "203.0.113.9"},
		{"last header line", "X-Forwarded-For", []string{"198.51.100.7", "203.0.113.9"}, "203.0.113.9"},
		{"forged left values ignored", "X-Forwarded-For", []string{"1.1.1.1,1.1.1.1, 203.0.113.9"}, "203.0.113.9"},
		{"ipv6", "X-Real-Ip", []string{"2001:db8::1"}, "2001:db8::1"},
		{"mapped ipv4", "X-Real-Ip", []string{"::ffff:203.0.113.9"}, "203.0.113.9"},
		{"missing header", "X-Forwarded-For", nil, "192.0.2.1"},
		{"garbage", "X-Forwarded-For", []string{"203.0.113.9, not-an-ip"}, "192.0.2.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/x", nil)
			r.RemoteAddr = "192.0.2.1:41000"
			for _, v := range tt.values {
				r.Header.Add("X-Forwarded-For", v)
				r.Header.Add("X-Real-Ip", v)
			}
			if got := ClientIP(tt.header)(r); got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

type fakeMembers map[uuid.UUID]membershipmodel.Member

func (f fakeMembers) Member(_ context.Context, id uuid.UUID) (membershipmodel.Member, error) {
	m, ok := f[id]
	if !ok {
		return membershipmodel.Member{}, membershipmodel.ErrMemberNotFound
	}
	return m, nil
}

// fakeKratosAdmin serves GET /admin/identities/{id} for the identities in phones.
func fakeKratosAdmin(t *testing.T, phones map[uuid.UUID]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/identities/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		phone, ok := phones[id]
		if err != nil || !ok {
			http.Error(w, `{"error":{"code":404}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if phone == "" {
			_, _ = fmt.Fprintf(w, `{"id":%q,"traits":{}}`, id)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"traits":{"phone":%q}}`, id, phone)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRecipients(t *testing.T) {
	memberID, identity := uuid.New(), uuid.New()
	noPhoneMember, noPhoneIdentity := uuid.New(), uuid.New()
	goneMember := uuid.New()
	members := fakeMembers{
		memberID:      {ID: memberID, IdentityID: identity, Lang: membershipmodel.LangRU},
		noPhoneMember: {ID: noPhoneMember, IdentityID: noPhoneIdentity, Lang: membershipmodel.LangKK},
		goneMember:    {ID: goneMember, IdentityID: uuid.New(), Lang: membershipmodel.LangKK},
	}
	srv := fakeKratosAdmin(t, map[uuid.UUID]string{identity: testPhone, noPhoneIdentity: ""})
	phones, err := NewKratosIdentities(srv.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	rc := &Recipients{Members: members, Phones: phones}
	ctx := context.Background()

	got, err := rc.Resolve(ctx, notifymodel.Recipient{MemberID: &memberID})
	if err != nil {
		t.Fatal(err)
	}
	want := notifymodel.ResolvedRecipient{Kind: notifymodel.RecipientKindMember, Ref: memberID, Language: "ru"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Resolve = %+v, want %+v", got, want)
	}
	phone, err := rc.PhoneFor(ctx, notifymodel.RecipientKindMember, memberID)
	if err != nil || phone != testPhone {
		t.Fatalf("PhoneFor = %q, %v; want %q", phone, err, testPhone)
	}
	for name, id := range map[string]uuid.UUID{"no phone trait": noPhoneMember, "identity gone": goneMember} {
		got, err := rc.Resolve(ctx, notifymodel.Recipient{MemberID: &id})
		if err != nil || len(got) != 1 || got[0].Ref != id {
			t.Errorf("%s: Resolve = %+v, %v; want the member", name, got, err)
		}
		phone, err := rc.PhoneFor(ctx, notifymodel.RecipientKindMember, id)
		if err != nil || phone != "" {
			t.Errorf("%s: PhoneFor = %q, %v; want empty phone", name, phone, err)
		}
	}

	unknown, contact := uuid.New(), uuid.New()
	for name, r := range map[string]notifymodel.Recipient{
		"unknown member":  {MemberID: &unknown},
		"trusted contact": {TrustedContactID: &contact},
		"staff role":      {StaffRole: notifymodel.StaffModerator},
	} {
		if _, err := rc.Resolve(ctx, r); !errors.Is(err, notifymodel.ErrRecipientUnresolved) {
			t.Errorf("%s: err = %v, want ErrRecipientUnresolved", name, err)
		}
	}
	if _, err := rc.PhoneFor(ctx, notifymodel.RecipientKindMember, unknown); !errors.Is(err, notifymodel.ErrRecipientUnresolved) {
		t.Errorf("PhoneFor unknown member: err = %v, want ErrRecipientUnresolved", err)
	}
}

func TestKratosIdentitiesFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	k, err := NewKratosIdentities(srv.URL, "phone", nil)
	if err != nil {
		t.Fatal(err)
	}
	// An outage is an error (the caller retries), not "no phone".
	if _, err := k.Phone(context.Background(), uuid.New()); err == nil {
		t.Fatal("Phone succeeded on a 500")
	}
	if _, err := NewKratosIdentities("kratos-admin", "", nil); err == nil {
		t.Fatal("relative admin URL accepted")
	}
	memberID, identity := uuid.New(), uuid.New()
	rc := &Recipients{
		Members: fakeMembers{memberID: {ID: memberID, IdentityID: identity, Lang: membershipmodel.LangRU}},
		Phones:  k,
	}
	if _, err := rc.Resolve(context.Background(), notifymodel.Recipient{MemberID: &memberID}); err != nil {
		t.Fatalf("Resolve must not call Kratos: %v", err)
	}
	if _, err := rc.PhoneFor(context.Background(), notifymodel.RecipientKindMember, memberID); err == nil {
		t.Fatal("PhoneFor succeeded while Kratos returned 500")
	}
}

func TestLogSendersLogNoContent(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	ctx := context.Background()
	id := uuid.New()
	if _, err := (LogSMSSender{Logger: logger}).Send(ctx, notifyport.SMSMessage{NotificationID: id, PhoneE164: testPhone, Text: "neutral-text"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (LogPushSender{Logger: logger}).Send(ctx, notifyport.PushMessage{
		NotificationID: id, PushToken: "push-token-value", Params: map[string]string{"k": "param-value"},
	}); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	for _, secret := range []string{testPhone, "neutral-text", "push-token-value", "param-value"} {
		if strings.Contains(out, secret) {
			t.Errorf("log contains %q: %s", secret, out)
		}
	}
	if strings.Count(out, id.String()) != 2 || !strings.Contains(out, `"channel":"sms"`) || !strings.Contains(out, `"channel":"push"`) {
		t.Errorf("log lacks notification id or channel: %s", out)
	}
}

func TestMergeHandlersRejectsDuplicates(t *testing.T) {
	noop := func(context.Context, pgx.Tx, timers.Due) error { return nil }
	merged, err := mergeHandlers(map[string]timers.Handler{"a.One": noop}, map[string]timers.Handler{"b.Two": noop})
	if err != nil || len(merged) != 2 {
		t.Fatalf("merge = %d handlers, %v; want 2, nil", len(merged), err)
	}
	if _, err := mergeHandlers(map[string]timers.Handler{"a.One": noop}, map[string]timers.Handler{"a.One": noop}); err == nil {
		t.Fatal("duplicate timer kind accepted")
	}
}
