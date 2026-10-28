package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/notifytest"
	"birge/internal/domains/notify/port"
	"birge/internal/domains/notify/service"
)

// deliver creates one notification for member and returns it.
func deliver(t *testing.T, svc *service.Service, store *notifytest.MemoryStore, member uuid.UUID, class string) model.Notification {
	t.Helper()
	reqID := uuid.Must(uuid.NewV7())
	if err := svc.HandleDeliveryRequested(context.Background(), uuid.Must(uuid.NewV7()), model.DeliveryRequest{
		RequestID: reqID,
		Recipient: model.Recipient{MemberID: &member},
		Class:     class,
		Template:  "emergency.sos",
	}); err != nil {
		t.Fatal(err)
	}
	n, ok := store.NotificationByRequest(reqID, model.RecipientKindMember, member)
	if !ok {
		t.Fatal("missing notification")
	}
	return n
}

func registerDevice(t *testing.T, svc *service.Service, member uuid.UUID, platform, token string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if err := svc.RegisterDevice(context.Background(), member, id, platform, token); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPushProviderErrors(t *testing.T) {
	t.Parallel()
	transient := errors.New("fcm: status 503")
	tests := []struct {
		name        string
		err         error
		wantErr     bool
		wantStatus  string
		wantDevices int
	}{
		{"success", nil, false, model.StatusSent, 1},
		{"transient retries", transient, true, model.StatusFailed, 1},
		{"invalid token removes the device", fmt.Errorf("fcm: UNREGISTERED: %w", port.ErrTokenInvalid), false, model.StatusFailed, 0},
		{"permanent keeps the device and does not retry", fmt.Errorf("fcm: INVALID_ARGUMENT: %w", port.ErrPermanent), false, model.StatusFailed, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clk := &fixedClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
			svc, store, _, push, member := newLadderService(t, clk)
			registerDevice(t, svc, member, model.PlatformAndroid, "token-android-1")
			push.Err = tt.err
			n := deliver(t, svc, store, member, model.ClassNormal)

			err := svc.HandlePush(context.Background(), n.ID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HandlePush error = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, transient) {
				t.Errorf("HandlePush error = %v, want it to wrap the provider error", err)
			}
			if status, _ := store.DeliveryStatus(n.ID, model.ChannelPush); status != tt.wantStatus {
				t.Errorf("push status = %q, want %q", status, tt.wantStatus)
			}
			if got := store.DeviceCount(member); got != tt.wantDevices {
				t.Errorf("devices = %d, want %d", got, tt.wantDevices)
			}
		})
	}
}

func TestPushRetryAfterTransientFailureSends(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	svc, store, _, push, member := newLadderService(t, clk)
	registerDevice(t, svc, member, model.PlatformIOS, "token-ios-1")
	push.Err = errors.New("apns: status 429")
	n := deliver(t, svc, store, member, model.ClassCritical)

	if err := svc.HandlePush(context.Background(), n.ID); err == nil {
		t.Fatal("first attempt: want an error so the timer retries")
	}
	push.Err = nil
	if err := svc.HandlePush(context.Background(), n.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if status, _ := store.DeliveryStatus(n.ID, model.ChannelPush); status != model.StatusSent {
		t.Errorf("push status = %q, want sent", status)
	}
	if push.Len() != 2 {
		t.Errorf("push sends = %d, want 2", push.Len())
	}
}

func TestPushRejectedTokenRemovedEvenWhenAnotherDeviceReceives(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	svc, store, _, push, member := newLadderService(t, clk)
	registerDevice(t, svc, member, model.PlatformAndroid, "token-dead-1")
	registerDevice(t, svc, member, model.PlatformIOS, "token-live-1")
	push.ErrByToken = map[string]error{
		"token-dead-1": fmt.Errorf("fcm: %w", port.ErrTokenInvalid),
		"token-live-1": nil,
	}
	n := deliver(t, svc, store, member, model.ClassNormal)
	if err := svc.HandlePush(context.Background(), n.ID); err != nil {
		t.Fatal(err)
	}
	if status, _ := store.DeliveryStatus(n.ID, model.ChannelPush); status != model.StatusSent {
		t.Errorf("push status = %q, want sent", status)
	}
	// Device order is not fixed: the dead token is removed only if it was
	// tried before the live one.
	want := 2
	if push.Calls[0].PushToken == "token-dead-1" {
		want = 1
	}
	if got := store.DeviceCount(member); got != want {
		t.Errorf("devices = %d, want %d", got, want)
	}
}

func TestDeleteRejectedDeviceKeepsReRegisteredToken(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	svc, store, _, _, member := newLadderService(t, clk)
	device := registerDevice(t, svc, member, model.PlatformAndroid, "token-old-1")
	// The app registers a fresh token before the rejection is processed.
	if err := svc.RegisterDevice(context.Background(), member, device, model.PlatformAndroid, "token-new-1"); err != nil {
		t.Fatal(err)
	}
	err := store.InTx(context.Background(), func(tx port.Tx) error {
		removed, err := tx.DeleteRejectedDevice(context.Background(), member, device, "token-old-1")
		if removed {
			t.Error("removed a registration that holds a different token")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.DeviceCount(member) != 1 {
		t.Error("re-registered device was removed")
	}
}

func TestPushTitle(t *testing.T) {
	t.Parallel()
	titles := map[string]string{"kk": "Бірге: жаңа хабар", "en": "Birge: new notification"}
	tests := []struct {
		name   string
		titles map[string]string
		lang   string
		want   string
	}{
		{"member language", titles, "kk", "Бірге: жаңа хабар"},
		{"fallback order ru kk en", titles, "ru", "Бірге: жаңа хабар"},
		{"unset falls back to the product name", nil, "ru", service.DefaultPushTitle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := service.PushTitle(tt.titles, tt.lang); got != tt.want {
				t.Errorf("PushTitle = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPushCarriesTitleInMemberLanguage(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	svc, store, _, push, member := newLadderService(t, clk)
	svc.Config.PushTitles = map[string]string{"ru": "TEST ru title", "en": "TEST en title"}
	registerDevice(t, svc, member, model.PlatformAndroid, "token-android-1")
	n := deliver(t, svc, store, member, model.ClassNormal)
	if err := svc.HandlePush(context.Background(), n.ID); err != nil {
		t.Fatal(err)
	}
	if push.Len() != 1 || push.Calls[0].Title != "TEST ru title" {
		t.Fatalf("push calls = %+v, want one with the ru title", push.Calls)
	}
}

func TestSMSProviderErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		err     error
		wantErr bool
	}{
		{"transient retries", errors.New("smsc: status 503"), true},
		{"permanent does not retry", fmt.Errorf("smsc: error code 7: %w", port.ErrPermanent), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
			clk := &fixedClock{t: start}
			svc, store, sms, _, member := newLadderService(t, clk)
			sms.Err = tt.err
			n := deliver(t, svc, store, member, model.ClassCritical)
			clk.t = start.Add(2 * time.Minute)

			err := svc.HandleCriticalSMS(context.Background(), n.ID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HandleCriticalSMS error = %v, want error %v", err, tt.wantErr)
			}
			if status, _ := store.DeliveryStatus(n.ID, model.ChannelSMS); status != model.StatusFailed {
				t.Errorf("sms status = %q, want failed", status)
			}
		})
	}
}
