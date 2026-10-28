package service_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/notifytest"
	"birge/internal/domains/notify/port"
	"birge/internal/domains/notify/service"
	"birge/internal/domains/notify/sms/guard"
)

const (
	signInPhone = "+77001234567"
	signInCode  = "482913"
)

// signInService has no Store and no Recipients: a sign-in SMS that touched
// either (and so could persist the code) panics on the nil interface.
func signInService(sms *notifytest.FakeSMS, g *guard.Guard, logs *bytes.Buffer) *service.Service {
	return &service.Service{
		SMS:         sms,
		SignInGuard: g,
		Clock:       fixedClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)},
		Logger:      slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func TestSendSignInCode(t *testing.T) {
	t.Parallel()
	open := func() *guard.Guard { return guard.New(guard.Config{AllowedPrefixes: []string{"+77"}}) }
	tests := []struct {
		name      string
		phone     string
		code      string
		guard     func() *guard.Guard
		smsErr    error
		wantErr   error
		wantSends int
	}{
		{"sent", signInPhone, signInCode, open, nil, nil, 1},
		{"prefix refused", "+19005550100", signInCode, open, nil, service.ErrSMSRefused, 0},
		{"daily budget refused", signInPhone, signInCode, func() *guard.Guard {
			g := guard.New(guard.Config{AllowedPrefixes: []string{"+77"}, DailyBudget: 1})
			_ = g.Allow("+77009999999", time.Date(2026, 10, 28, 11, 0, 0, 0, time.UTC))
			return g
		}, nil, service.ErrSMSRefused, 0},
		{"per-number limit refused", signInPhone, signInCode, func() *guard.Guard {
			g := guard.New(guard.Config{AllowedPrefixes: []string{"+77"}, PerNumberLimit: 1})
			_ = g.Allow(signInPhone, time.Date(2026, 10, 28, 11, 59, 0, 0, time.UTC))
			return g
		}, nil, service.ErrSMSRefused, 0},
		{"no guard refuses", signInPhone, signInCode, func() *guard.Guard { return nil }, nil, errAny, 0},
		{"code with text", signInPhone, "48291 hi", open, nil, model.ErrInvalidInput, 0},
		{"empty code", signInPhone, "", open, nil, model.ErrInvalidInput, 0},
		{"provider permanent", signInPhone, signInCode, open, fmt.Errorf("smsc: code 7: %w", port.ErrPermanent), port.ErrPermanent, 1},
		{"provider transient", signInPhone, signInCode, open, errors.New("smsc: status 502"), errAny, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			sms := &notifytest.FakeSMS{Err: tt.smsErr}
			svc := signInService(sms, tt.guard(), &logs)
			err := svc.SendSignInCode(context.Background(), tt.phone, tt.code)
			switch {
			case tt.wantErr == nil:
				if err != nil {
					t.Fatalf("err = %v", err)
				}
			case errors.Is(tt.wantErr, errAny):
				if err == nil {
					t.Fatal("want an error")
				}
			case !errors.Is(err, tt.wantErr):
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if sms.Len() != tt.wantSends {
				t.Fatalf("sends = %d, want %d", sms.Len(), tt.wantSends)
			}
			if tt.wantSends == 1 {
				got := sms.Calls[0]
				if got.PhoneE164 != tt.phone || got.Text != service.SignInCodeText(signInCode) {
					t.Fatal("the provider did not get the number and the fixed code text")
				}
			}
			for _, secret := range []string{signInCode, signInPhone, "7001234567", "48291"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("log contains %q: %s", secret, logs.String())
				}
				if err != nil && strings.Contains(err.Error(), secret) {
					t.Fatalf("error contains %q: %v", secret, err)
				}
			}
		})
	}
}

// The sign-in guard is its own: exhausting it leaves the notification
// guard, which critical and plan SMS use, untouched.
func TestSendSignInCodeKeepsNotificationBudget(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	cfg := guard.Config{AllowedPrefixes: []string{"+77"}, DailyBudget: 1, PerNumberLimit: 1}
	svc := signInService(&notifytest.FakeSMS{}, guard.New(cfg), &logs)
	svc.Guard = guard.New(cfg)
	ctx := context.Background()
	if err := svc.SendSignInCode(ctx, signInPhone, signInCode); err != nil {
		t.Fatal(err)
	}
	if err := svc.SendSignInCode(ctx, signInPhone, signInCode); !errors.Is(err, service.ErrSMSRefused) {
		t.Fatalf("second sign-in SMS: err = %v, want refused", err)
	}
	if err := svc.Guard.Allow(signInPhone, svc.Clock.Now()); err != nil {
		t.Fatalf("notification guard was used by sign-in SMS: %v", err)
	}
}

func TestValidSignInCode(t *testing.T) {
	t.Parallel()
	for code, want := range map[string]bool{
		"1234": true, "482913": true, "0123456789": true,
		"": false, "123": false, "01234567890": false, "12a4": false, "12 34": false, "１２３４": false,
	} {
		if got := model.ValidSignInCode(code); got != want {
			t.Errorf("ValidSignInCode(%q) = %v, want %v", code, got, want)
		}
	}
}

var errAny = errors.New("any error")
