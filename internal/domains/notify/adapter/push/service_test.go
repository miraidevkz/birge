package push_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/notify/adapter/push"
	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/notifytest"
	"birge/internal/domains/notify/service"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// TestServiceWithFCM runs the notify service against the fake FCM and checks
// the delivery outcome and that the log never holds a token.
func TestServiceWithFCM(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      int
		body        string
		wantErr     bool
		wantStatus  string
		wantDevices int
	}{
		{"delivered", 200, "", false, model.StatusSent, 1},
		{"unregistered token removes the device", 404, `{"error":{"code":404,"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`, false, model.StatusFailed, 0},
		{"rate limited retries", 429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`, true, model.StatusFailed, 1},
		{"server error retries", 503, `{"error":{"code":503,"status":"UNAVAILABLE"}}`, true, model.StatusFailed, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fcm, fake, _ := newTestFCM(t)
			fake.set(tt.status, tt.body)
			var logs bytes.Buffer
			store := notifytest.NewMemoryStore()
			member := uuid.Must(uuid.NewV7())
			svc := &service.Service{
				Store:      store,
				Recipients: &notifytest.FakeRecipients{},
				Push:       push.Router{Android: fcm},
				Clock:      fixedClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)},
				Logger:     slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
			}
			ctx := context.Background()
			if err := svc.RegisterDevice(ctx, member, uuid.Must(uuid.NewV7()), model.PlatformAndroid, testDeviceToken); err != nil {
				t.Fatal(err)
			}
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

			err := svc.HandlePush(ctx, n.ID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HandlePush error = %v, want error %v", err, tt.wantErr)
			}
			if status, _ := store.DeliveryStatus(n.ID, model.ChannelPush); status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if got := store.DeviceCount(member); got != tt.wantDevices {
				t.Errorf("devices = %d, want %d", got, tt.wantDevices)
			}
			fake.mu.Lock()
			msg := fake.lastMessage
			fake.mu.Unlock()
			if title := msg["notification"].(map[string]any)["title"]; title != service.DefaultPushTitle {
				t.Errorf("title = %v, want the default", title)
			}
			out := logs.String()
			for _, secret := range []string{testDeviceToken, "TEST-access", "emergency.sos_raised"} {
				if strings.Contains(out, secret) {
					t.Errorf("log contains %q:\n%s", secret, out)
				}
			}
			if err != nil && strings.Contains(err.Error(), testDeviceToken) {
				t.Errorf("error leaks the token: %v", err)
			}
		})
	}
}
