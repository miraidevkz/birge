package authgw

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const courierSecret = "courier-secret-0123456789abcdef-0123456789"

type fakeCodeSender struct {
	mu    sync.Mutex
	err   error
	calls []string // phone + "|" + code
}

func (f *fakeCodeSender) SendSignInCode(_ context.Context, phone, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, phone+"|"+code)
	return f.err
}

func newTestHook(t *testing.T, sender CodeSender) (*CourierHook, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	h, err := NewCourierHook(CourierConfig{
		Secret:          []byte(courierSecret),
		AllowedPrefixes: []string{"+7700", "+7701"},
		Sender:          sender,
		Logger:          slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, &logs
}

func courierBody(to, template, code string) string {
	return fmt.Sprintf(`{"to":%q,"template":%q,"code":%q}`, to, template, code)
}

func TestCourierHook(t *testing.T) {
	t.Parallel()
	valid := courierBody(allowedNumber, TemplateLoginCode, smsCode)
	tests := []struct {
		name       string
		method     string
		auth       string
		body       string
		senderErr  error
		wantStatus int
		wantSent   bool
	}{
		{"login code sent", http.MethodPost, "Bearer " + courierSecret, valid, nil, http.StatusNoContent, true},
		{"registration code sent", http.MethodPost, "Bearer " + courierSecret,
			courierBody(otherAllowed, TemplateRegistrationCode, smsCode), nil, http.StatusNoContent, true},
		{"missing secret", http.MethodPost, "", valid, nil, http.StatusUnauthorized, false},
		{"wrong secret", http.MethodPost, "Bearer " + courierSecret + "x", valid, nil, http.StatusUnauthorized, false},
		{"secret without bearer", http.MethodPost, courierSecret, valid, nil, http.StatusUnauthorized, false},
		{"empty bearer", http.MethodPost, "Bearer ", valid, nil, http.StatusUnauthorized, false},
		{"wrong method", http.MethodGet, "Bearer " + courierSecret, "", nil, http.StatusMethodNotAllowed, false},
		{"verification template refused", http.MethodPost, "Bearer " + courierSecret,
			courierBody(allowedNumber, "verification_code_valid", smsCode), nil, http.StatusUnprocessableEntity, false},
		{"recovery template refused", http.MethodPost, "Bearer " + courierSecret,
			courierBody(allowedNumber, "recovery_code_valid", smsCode), nil, http.StatusUnprocessableEntity, false},
		{"empty template refused", http.MethodPost, "Bearer " + courierSecret,
			courierBody(allowedNumber, "", smsCode), nil, http.StatusUnprocessableEntity, false},
		{"number not E.164", http.MethodPost, "Bearer " + courierSecret,
			courierBody("87001234567", TemplateLoginCode, smsCode), nil, http.StatusUnprocessableEntity, false},
		{"number outside allowed ranges", http.MethodPost, "Bearer " + courierSecret,
			courierBody(foreignNumber, TemplateLoginCode, smsCode), nil, http.StatusUnprocessableEntity, false},
		{"code with text", http.MethodPost, "Bearer " + courierSecret,
			courierBody(allowedNumber, TemplateLoginCode, "4829 call me"), nil, http.StatusBadRequest, false},
		{"code too short", http.MethodPost, "Bearer " + courierSecret,
			courierBody(allowedNumber, TemplateLoginCode, "12"), nil, http.StatusBadRequest, false},
		{"unknown field", http.MethodPost, "Bearer " + courierSecret,
			`{"to":"+77001234567","template":"login_code_valid","code":"482913","body":"x"}`, nil, http.StatusBadRequest, false},
		{"two objects", http.MethodPost, "Bearer " + courierSecret, valid + valid, nil, http.StatusBadRequest, false},
		{"not json", http.MethodPost, "Bearer " + courierSecret, "Birge: 482913", nil, http.StatusBadRequest, false},
		{"guard refusal fails to kratos", http.MethodPost, "Bearer " + courierSecret, valid,
			fmt.Errorf("notify: %w", ErrCodeRefused), http.StatusTooManyRequests, true},
		{"provider permanent", http.MethodPost, "Bearer " + courierSecret, valid,
			fmt.Errorf("smsc: code 7: %w", ErrCodePermanent), http.StatusUnprocessableEntity, true},
		{"provider transient", http.MethodPost, "Bearer " + courierSecret, valid,
			errors.New("smsc: status 502"), http.StatusServiceUnavailable, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sender := &fakeCodeSender{err: tt.senderErr}
			h, logs := newTestHook(t, sender)
			req := httptest.NewRequest(tt.method, CourierPath, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := len(sender.calls) == 1; got != tt.wantSent {
				t.Fatalf("sender calls = %d, want sent %v", len(sender.calls), tt.wantSent)
			}
			if tt.wantSent {
				if !strings.HasSuffix(sender.calls[0], "|"+smsCode) {
					t.Fatal("the sender did not get the code from Kratos")
				}
			}
			for _, secret := range []string{smsCode, allowedNumber, otherAllowed, foreignNumber, "7001234567", courierSecret, "call me"} {
				if strings.Contains(logs.String(), secret) || strings.Contains(rec.Body.String(), secret) {
					t.Fatalf("log or answer contains %q:\nlog: %s\nbody: %s", secret, logs.String(), rec.Body.String())
				}
			}
		})
	}
}

func TestCourierHookPassesExactValues(t *testing.T) {
	t.Parallel()
	sender := &fakeCodeSender{}
	h, _ := newTestHook(t, sender)
	req := httptest.NewRequest(http.MethodPost, CourierPath,
		strings.NewReader(courierBody(allowedNumber, TemplateLoginCode, smsCode)))
	req.Header.Set("Authorization", "Bearer "+courierSecret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if want := allowedNumber + "|" + smsCode; len(sender.calls) != 1 || sender.calls[0] != want {
		t.Fatalf("sender got %v", sender.calls)
	}
}

func TestCourierHookRefusesLargeBody(t *testing.T) {
	t.Parallel()
	sender := &fakeCodeSender{}
	h, _ := newTestHook(t, sender)
	big := `{"to":"` + allowedNumber + `","template":"login_code_valid","code":"` + smsCode + `","pad":"` + strings.Repeat("a", 8<<10) + `"}`
	req := httptest.NewRequest(http.MethodPost, CourierPath, strings.NewReader(big))
	req.Header.Set("Authorization", "Bearer "+courierSecret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || len(sender.calls) != 0 {
		t.Fatalf("status = %d, calls = %d", rec.Code, len(sender.calls))
	}
}

func TestNewCourierHookValidates(t *testing.T) {
	t.Parallel()
	sender := &fakeCodeSender{}
	tests := []struct {
		name string
		cfg  CourierConfig
	}{
		{"short secret", CourierConfig{Secret: []byte("short"), AllowedPrefixes: []string{"+77"}, Sender: sender}},
		{"no prefixes", CourierConfig{Secret: []byte(courierSecret), Sender: sender}},
		{"bad prefix", CourierConfig{Secret: []byte(courierSecret), AllowedPrefixes: []string{"77"}, Sender: sender}},
		{"no sender", CourierConfig{Secret: []byte(courierSecret), AllowedPrefixes: []string{"+77"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewCourierHook(tt.cfg); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
