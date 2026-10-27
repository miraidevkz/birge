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

	emergencyservice "birge/internal/domains/emergency/service"
	membershipmodel "birge/internal/domains/membership/model"
	notifymodel "birge/internal/domains/notify/model"
	notifyport "birge/internal/domains/notify/port"
	safetymodel "birge/internal/domains/safety/model"
	safetyservice "birge/internal/domains/safety/service"
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

type fakeTrustedContact struct {
	memberID *uuid.UUID
	external bool
	phone    string
	lang     string
	err      error
}

type fakeEmergency struct {
	byID map[uuid.UUID]fakeTrustedContact
}

func (f *fakeEmergency) TrustedContactTarget(_ context.Context, id uuid.UUID) (*uuid.UUID, bool, error) {
	c, ok := f.byID[id]
	if !ok {
		return nil, false, emergencyservice.ErrNotFound
	}
	if c.err != nil {
		return nil, false, c.err
	}
	return c.memberID, c.external, nil
}

func (f *fakeEmergency) ContactPhone(_ context.Context, id uuid.UUID) (string, string, error) {
	c, ok := f.byID[id]
	if !ok || !c.external {
		return "", "", emergencyservice.ErrNotFound
	}
	if c.err != nil {
		return "", "", c.err
	}
	return c.phone, c.lang, nil
}

type fakeStaff struct {
	byRole map[string][]safetyservice.ExternalRecipient
	phones map[uuid.UUID]string
	err    error
}

func (f *fakeStaff) ResolveStaffRole(_ context.Context, role string) ([]safetyservice.ExternalRecipient, error) {
	if f.err != nil {
		return nil, f.err
	}
	got, ok := f.byRole[role]
	if !ok || len(got) == 0 {
		return nil, safetymodel.ErrNoDutyCoverage
	}
	return got, nil
}

func (f *fakeStaff) ShiftPhone(_ context.Context, shiftID uuid.UUID) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	phone, ok := f.phones[shiftID]
	if !ok {
		return "", safetymodel.ErrNotFound
	}
	return phone, nil
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
	shiftID := uuid.New()
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
	staff := &fakeStaff{
		byRole: map[string][]safetyservice.ExternalRecipient{
			notifymodel.StaffPsychologist: {{Ref: shiftID, PhoneE164: testPhone, Lang: "ru"}},
		},
		phones: map[uuid.UUID]string{shiftID: testPhone},
	}
	extContact, memberContact, removedContact := uuid.New(), uuid.New(), uuid.New()
	extPhone := "+77001112233"
	emergency := &fakeEmergency{byID: map[uuid.UUID]fakeTrustedContact{
		extContact:    {external: true, phone: extPhone, lang: "kk"},
		memberContact: {memberID: &memberID, external: false},
	}}
	rc := &Recipients{Members: members, Phones: phones, Staff: staff, Emergency: emergency, StaffLang: "ru"}
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

	staffGot, err := rc.Resolve(ctx, notifymodel.Recipient{StaffRole: notifymodel.StaffPsychologist})
	if err != nil || len(staffGot) != 1 || staffGot[0].Kind != notifymodel.RecipientKindStaff || staffGot[0].Ref != shiftID {
		t.Fatalf("staff Resolve = %+v, %v", staffGot, err)
	}
	phone, err = rc.PhoneFor(ctx, notifymodel.RecipientKindStaff, shiftID)
	if err != nil || phone != testPhone {
		t.Fatalf("staff PhoneFor = %q, %v", phone, err)
	}
	shiftRef := shiftID
	re, err := rc.Resolve(ctx, notifymodel.Recipient{StaffShiftID: &shiftRef})
	if err != nil || len(re) != 1 || re[0].Kind != notifymodel.RecipientKindStaff || re[0].Language != "ru" {
		t.Fatalf("StaffShiftID Resolve = %+v, %v", re, err)
	}

	extGot, err := rc.Resolve(ctx, notifymodel.Recipient{TrustedContactID: &extContact})
	if err != nil || len(extGot) != 1 || extGot[0].Kind != notifymodel.RecipientKindExternal ||
		extGot[0].Ref != extContact || extGot[0].Language != "kk" {
		t.Fatalf("external trusted Resolve = %+v, %v", extGot, err)
	}
	phone, err = rc.PhoneFor(ctx, notifymodel.RecipientKindExternal, extContact)
	if err != nil || phone != extPhone {
		t.Fatalf("external PhoneFor = %q, %v", phone, err)
	}
	memGot, err := rc.Resolve(ctx, notifymodel.Recipient{TrustedContactID: &memberContact})
	if err != nil || len(memGot) != 1 || memGot[0].Kind != notifymodel.RecipientKindMember || memGot[0].Ref != memberID {
		t.Fatalf("member trusted Resolve = %+v, %v", memGot, err)
	}

	unknown := uuid.New()
	for name, r := range map[string]notifymodel.Recipient{
		"unknown member":  {MemberID: &unknown},
		"removed contact": {TrustedContactID: &removedContact},
		"no duty":         {StaffRole: notifymodel.StaffModerator},
	} {
		if _, err := rc.Resolve(ctx, r); !errors.Is(err, notifymodel.ErrRecipientUnresolved) {
			t.Errorf("%s: err = %v, want ErrRecipientUnresolved", name, err)
		}
	}
	if _, err := rc.PhoneFor(ctx, notifymodel.RecipientKindMember, unknown); !errors.Is(err, notifymodel.ErrRecipientUnresolved) {
		t.Errorf("PhoneFor unknown member: err = %v, want ErrRecipientUnresolved", err)
	}
	if _, err := rc.PhoneFor(ctx, notifymodel.RecipientKindExternal, removedContact); !errors.Is(err, notifymodel.ErrRecipientUnresolved) {
		t.Errorf("PhoneFor removed contact: err = %v, want ErrRecipientUnresolved", err)
	}
	phone, err = rc.PhoneFor(ctx, notifymodel.RecipientKindStaff, uuid.New())
	if err != nil || phone != "" {
		t.Errorf("PhoneFor cancelled/unknown shift = %q, %v; want empty", phone, err)
	}
}

func TestFirstName(t *testing.T) {
	for in, want := range map[string]string{
		"Example Name": "Example",
		"Single":       "Single",
		"  A B C  ":    "A",
		"":             "",
	} {
		if got := firstName(in); got != want {
			t.Errorf("firstName(%q) = %q, want %q", in, got, want)
		}
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
