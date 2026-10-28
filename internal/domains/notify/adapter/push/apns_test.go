package push_test

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"birge/internal/domains/notify/adapter/push"
	"birge/internal/domains/notify/port"
)

const (
	testKeyID  = "TESTKEY001"
	testTeamID = "TESTTEAM01"
	testTopic  = "kz.example.birge.test"
)

// fakeAPNs is an HTTP/2 TLS httptest APNs.
type fakeAPNs struct {
	t   *testing.T
	pub *ecdsa.PublicKey
	srv *httptest.Server

	mu       sync.Mutex
	status   int
	reason   string
	jwts     []string
	lastBody map[string]any
	lastHdr  http.Header
	lastPath string
}

func newFakeAPNs(t *testing.T, pub *ecdsa.PublicKey) *fakeAPNs {
	f := &fakeAPNs{t: t, pub: pub, status: http.StatusOK}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(f.handle))
	f.srv.EnableHTTP2 = true
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPNs) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.ProtoMajor != 2 {
		f.t.Errorf("APNs request over %s, want HTTP/2", r.Proto)
	}
	auth := r.Header.Get("Authorization")
	jwt, ok := strings.CutPrefix(auth, "bearer ")
	if !ok {
		f.t.Errorf("authorization = %q", auth)
	}
	header, claims, signing, sig := jwtParts(f.t, jwt)
	if header["alg"] != "ES256" || header["kid"] != testKeyID || claims["iss"] != testTeamID {
		f.t.Errorf("provider token header %v claims %v", header, claims)
	}
	verifyES256(f.t, f.pub, signing, sig)
	if len(f.jwts) == 0 || f.jwts[len(f.jwts)-1] != jwt {
		f.jwts = append(f.jwts, jwt)
	}
	f.lastHdr = r.Header.Clone()
	f.lastPath = r.URL.Path
	f.lastBody = nil
	_ = json.NewDecoder(r.Body).Decode(&f.lastBody)
	if f.status == http.StatusOK {
		w.Header().Set("apns-id", r.Header.Get("apns-id"))
		return
	}
	w.WriteHeader(f.status)
	_, _ = io.WriteString(w, fmt.Sprintf(`{"reason":%q}`, f.reason))
}

func (f *fakeAPNs) set(status int, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.reason = status, reason
}

func (f *fakeAPNs) tokens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.jwts)
}

func newTestAPNs(t *testing.T) (*push.APNs, *fakeAPNs, *testClock) {
	t.Helper()
	key, p8 := testP8Key(t)
	fake := newFakeAPNs(t, &key.PublicKey)
	creds, err := push.ParseAPNsKey(p8, testKeyID, testTeamID)
	if err != nil {
		t.Fatal(err)
	}
	clk := newClock()
	return &push.APNs{
		Credentials: creds,
		Topic:       testTopic,
		BaseURL:     fake.srv.URL,
		Client:      fake.srv.Client(),
		Clock:       clk,
	}, fake, clk
}

func TestAPNsSendsContentFreeAlertOverHTTP2(t *testing.T) {
	t.Parallel()
	apns, fake, clk := newTestAPNs(t)
	msg := testPush()
	msg.Platform = "ios"

	id, err := apns.Send(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if id != msg.NotificationID.String() {
		t.Errorf("provider id = %q, want the apns-id echo", id)
	}
	fake.mu.Lock()
	if fake.lastPath != "/3/device/"+testDeviceToken {
		t.Errorf("path = %q", fake.lastPath)
	}
	for k, want := range map[string]string{"apns-topic": testTopic, "apns-push-type": "alert", "apns-priority": "10"} {
		if got := fake.lastHdr.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	body, _ := json.Marshal(fake.lastBody)
	fake.mu.Unlock()
	want := fmt.Sprintf(`{"aps":{"alert":{"title":"TEST title"},"sound":"default"},"notification_id":"%s"}`, msg.NotificationID)
	if string(body) != want {
		t.Errorf("payload = %s\nwant      %s", body, want)
	}

	// The provider token is reused inside Apple's window and renewed after 40 minutes.
	clk.Advance(39 * time.Minute)
	if _, err := apns.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if n := fake.tokens(); n != 1 {
		t.Errorf("provider tokens after 39m = %d, want 1", n)
	}
	clk.Advance(2 * time.Minute)
	if _, err := apns.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if n := fake.tokens(); n != 2 {
		t.Errorf("provider tokens after 41m = %d, want 2", n)
	}
}

func TestAPNsErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status int
		reason string
		want   error
	}{
		{410, "Unregistered", port.ErrTokenInvalid},
		{400, "BadDeviceToken", port.ErrTokenInvalid},
		{400, "DeviceTokenNotForTopic", port.ErrTokenInvalid},
		{413, "PayloadTooLarge", port.ErrPermanent},
		{400, "BadCollapseId", port.ErrPermanent},
		{400, "BadTopic", nil},
		{429, "TooManyRequests", nil},
		{503, "ServiceUnavailable", nil},
		{500, "InternalServerError", nil},
		{403, "InvalidProviderToken", nil},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			t.Parallel()
			apns, fake, _ := newTestAPNs(t)
			fake.set(tt.status, tt.reason)
			_, err := apns.Send(context.Background(), testPush())
			if err == nil {
				t.Fatal("Send succeeded")
			}
			checkClass(t, err, tt.want)
			if strings.Contains(err.Error(), testDeviceToken) {
				t.Errorf("error text leaks the device token: %v", err)
			}
		})
	}
}

func TestAPNsExpiredProviderTokenRenewsOutsideTheThrottleWindow(t *testing.T) {
	t.Parallel()
	apns, fake, clk := newTestAPNs(t)
	fake.set(403, "ExpiredProviderToken")
	// A fresh token Apple calls expired is kept (renewing would be throttled).
	if _, err := apns.Send(context.Background(), testPush()); err == nil {
		t.Fatal("Send succeeded")
	}
	if _, err := apns.Send(context.Background(), testPush()); err == nil {
		t.Fatal("Send succeeded")
	}
	if n := fake.tokens(); n != 1 {
		t.Errorf("provider tokens = %d, want 1 inside 20 minutes", n)
	}
	clk.Advance(21 * time.Minute)
	if _, err := apns.Send(context.Background(), testPush()); err == nil {
		t.Fatal("Send succeeded")
	}
	fake.set(200, "")
	if _, err := apns.Send(context.Background(), testPush()); err != nil {
		t.Fatal(err)
	}
	if n := fake.tokens(); n != 2 {
		t.Errorf("provider tokens = %d, want a renewed one after ExpiredProviderToken", n)
	}
}

func TestAPNsTransportErrorHidesToken(t *testing.T) {
	t.Parallel()
	apns, fake, _ := newTestAPNs(t)
	fake.srv.Close()
	_, err := apns.Send(context.Background(), testPush())
	if err == nil {
		t.Fatal("Send succeeded against a closed server")
	}
	checkClass(t, err, nil)
	if strings.Contains(err.Error(), testDeviceToken) {
		t.Errorf("transport error leaks the device token in the URL: %v", err)
	}
}

func TestParseAPNsKeyRejectsBadFiles(t *testing.T) {
	t.Parallel()
	_, p8 := testP8Key(t)
	_, rsaPEM := testRSAKey(t)
	tests := []struct {
		name          string
		p8            []byte
		keyID, teamID string
		want          string
	}{
		{"garbage", []byte("TEST-not-a-key"), testKeyID, testTeamID, "no PEM block"},
		{"rsa key", []byte(rsaPEM), testKeyID, testTeamID, "not an ECDSA P-256 key"},
		{"bad key id", p8, "short", testTeamID, "key ID"},
		{"bad team id", p8, testKeyID, "lower-case1", "team ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := push.ParseAPNsKey(tt.p8, tt.keyID, tt.teamID)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestRouterPicksProviderByPlatform(t *testing.T) {
	t.Parallel()
	apns, fakeA, _ := newTestAPNs(t)
	r := push.Router{IOS: apns}
	msg := testPush()
	msg.Platform = "ios"
	if _, err := r.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if fakeA.tokens() != 1 {
		t.Error("iOS push did not reach APNs")
	}
	msg.Platform = "android"
	_, err := r.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("android push without FCM succeeded")
	}
	checkClass(t, err, port.ErrPermanent)
}
