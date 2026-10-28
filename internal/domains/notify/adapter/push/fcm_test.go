package push_test

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/notify/adapter/push"
	"birge/internal/domains/notify/port"
)

const testDeviceToken = "TEST-device-token-0123456789"

// fakeFCM is an httptest FCM: a token endpoint and messages:send.
type fakeFCM struct {
	t   *testing.T
	pub *rsa.PublicKey
	srv *httptest.Server

	mu          sync.Mutex
	tokenCalls  int
	tokenStatus int
	sendStatus  int
	sendBody    string
	lastMessage map[string]any
	lastAuth    string
}

func newFakeFCM(t *testing.T, pub *rsa.PublicKey) *fakeFCM {
	f := &fakeFCM{t: t, pub: pub, tokenStatus: http.StatusOK, sendStatus: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", f.token)
	mux.HandleFunc("POST /v1/projects/test-project/messages:send", f.send)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFCM) token(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("token form: %v", err)
	}
	if got := r.PostForm.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		f.t.Errorf("grant_type = %q", got)
	}
	header, claims, signing, sig := jwtParts(f.t, r.PostForm.Get("assertion"))
	if header["alg"] != "RS256" || header["kid"] != "test-key-id" {
		f.t.Errorf("assertion header = %v", header)
	}
	verifyRS256(f.t, f.pub, signing, sig)
	if claims["iss"] != "notify@test-project.example.invalid" ||
		claims["aud"] != f.srv.URL+"/token" ||
		claims["scope"] != "https://www.googleapis.com/auth/firebase.messaging" {
		f.t.Errorf("assertion claims = %v", claims)
	}
	f.tokenCalls++
	if f.tokenStatus != http.StatusOK {
		w.WriteHeader(f.tokenStatus)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": fmt.Sprintf("TEST-access-%d", f.tokenCalls),
		"expires_in":   3600,
		"token_type":   "Bearer",
	})
}

func (f *fakeFCM) send(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastAuth = r.Header.Get("Authorization")
	var body struct {
		Message map[string]any `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("send body: %v", err)
	}
	f.lastMessage = body.Message
	w.WriteHeader(f.sendStatus)
	if f.sendStatus == http.StatusOK {
		_, _ = io.WriteString(w, `{"name":"projects/test-project/messages/0:1"}`)
		return
	}
	_, _ = io.WriteString(w, f.sendBody)
}

func (f *fakeFCM) set(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendStatus, f.sendBody = status, body
}

func (f *fakeFCM) calls() (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls, f.lastAuth
}

func serviceAccountJSON(t *testing.T, keyPEM, tokenURI string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "test-project",
		"private_key_id": "test-key-id",
		"private_key":    keyPEM,
		"client_email":   "notify@test-project.example.invalid",
		"token_uri":      tokenURI,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTestFCM(t *testing.T) (*push.FCM, *fakeFCM, *testClock) {
	t.Helper()
	key, keyPEM := testRSAKey(t)
	fake := newFakeFCM(t, &key.PublicKey)
	creds, err := push.ParseFCMServiceAccount(serviceAccountJSON(t, keyPEM, "https://oauth2.example.invalid/token"))
	if err != nil {
		t.Fatal(err)
	}
	clk := newClock()
	return &push.FCM{
		Credentials: creds,
		BaseURL:     fake.srv.URL,
		TokenURL:    fake.srv.URL + "/token",
		Client:      fake.srv.Client(),
		Clock:       clk,
	}, fake, clk
}

func testPush() port.PushMessage {
	return port.PushMessage{
		MemberID:       uuid.Must(uuid.NewV7()),
		DeviceID:       uuid.Must(uuid.NewV7()),
		PushToken:      testDeviceToken,
		Platform:       "android",
		NotificationID: uuid.Must(uuid.NewV7()),
		Title:          "TEST title",
		Template:       "emergency.sos_raised",
		Params:         map[string]string{"sos_id": "secret-param"},
		Class:          "critical",
	}
}

func TestFCMSendsContentFreeMessageAndCachesToken(t *testing.T) {
	t.Parallel()
	fcm, fake, clk := newTestFCM(t)
	msg := testPush()

	id, err := fcm.Send(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if id != "projects/test-project/messages/0:1" {
		t.Errorf("provider id = %q", id)
	}
	calls, auth := fake.calls()
	if calls != 1 || auth != "Bearer TEST-access-1" {
		t.Errorf("token calls = %d, auth = %q", calls, auth)
	}
	got, err := json.Marshal(fake.lastMessage)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`{"android":{"priority":"HIGH"},"data":{"notification_id":"%s"},"notification":{"title":"TEST title"},"token":"%s"}`,
		msg.NotificationID, testDeviceToken)
	if string(got) != want {
		t.Errorf("message = %s\nwant      %s", got, want)
	}

	// Cached: a second send within the hour does not fetch a token.
	clk.Advance(50 * time.Minute)
	if _, err := fcm.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if calls, _ := fake.calls(); calls != 1 {
		t.Errorf("token calls after 50m = %d, want 1", calls)
	}
	// Within five minutes of expiry the token is renewed.
	clk.Advance(6 * time.Minute)
	if _, err := fcm.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if calls, auth := fake.calls(); calls != 2 || auth != "Bearer TEST-access-2" {
		t.Errorf("after 56m: token calls = %d, auth = %q, want a renewed token", calls, auth)
	}
}

func TestFCMConcurrentSendsShareOneToken(t *testing.T) {
	t.Parallel()
	fcm, fake, _ := newTestFCM(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := fcm.Send(context.Background(), testPush()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if calls, _ := fake.calls(); calls != 1 {
		t.Errorf("token calls = %d, want 1", calls)
	}
}

func TestFCMErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   error // nil = transient
	}{
		{"unregistered", 404, `{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, port.ErrTokenInvalid},
		{"malformed token", 400, `{"error":{"code":400,"status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"message.token","description":"Invalid registration token"}]}]}}`, port.ErrTokenInvalid},
		{"sender mismatch", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"errorCode":"SENDER_ID_MISMATCH"}]}}`, port.ErrTokenInvalid},
		{"invalid payload", 400, `{"error":{"code":400,"status":"INVALID_ARGUMENT"}}`, port.ErrPermanent},
		{"quota", 429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","details":[{"errorCode":"QUOTA_EXCEEDED"}]}}`, nil},
		{"unavailable", 503, `{"error":{"code":503,"status":"UNAVAILABLE"}}`, nil},
		{"internal", 500, `not json`, nil},
		{"project permission", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED"}}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fcm, fake, _ := newTestFCM(t)
			fake.set(tt.status, tt.body)
			_, err := fcm.Send(context.Background(), testPush())
			if err == nil {
				t.Fatal("Send succeeded")
			}
			checkClass(t, err, tt.want)
			if strings.Contains(err.Error(), testDeviceToken) || strings.Contains(err.Error(), "TEST-access") {
				t.Errorf("error text leaks a token: %v", err)
			}
		})
	}
}

// checkClass asserts err wraps want, or neither class when want is nil.
func checkClass(t *testing.T, err, want error) {
	t.Helper()
	for _, class := range []error{port.ErrTokenInvalid, port.ErrPermanent} {
		if is := errors.Is(err, class); is != errors.Is(class, want) {
			t.Errorf("errors.Is(%v, %v) = %v", err, class, is)
		}
	}
}

func TestFCMUnauthorizedRenewsToken(t *testing.T) {
	t.Parallel()
	fcm, fake, _ := newTestFCM(t)
	fake.set(401, `{"error":{"code":401,"status":"UNAUTHENTICATED"}}`)
	if _, err := fcm.Send(context.Background(), testPush()); err == nil {
		t.Fatal("Send succeeded on 401")
	} else {
		checkClass(t, err, nil)
	}
	fake.set(200, "")
	if _, err := fcm.Send(context.Background(), testPush()); err != nil {
		t.Fatal(err)
	}
	if calls, auth := fake.calls(); calls != 2 || auth != "Bearer TEST-access-2" {
		t.Errorf("token calls = %d, auth = %q, want a fresh token after 401", calls, auth)
	}
}

func TestFCMRejectedAssertionIsTransient(t *testing.T) {
	t.Parallel()
	fcm, fake, _ := newTestFCM(t)
	fake.mu.Lock()
	fake.tokenStatus = http.StatusBadRequest
	fake.mu.Unlock()
	_, err := fcm.Send(context.Background(), testPush())
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("err = %v, want the token error code", err)
	}
	checkClass(t, err, nil)
}

func TestParseFCMServiceAccountRejectsBadFiles(t *testing.T) {
	t.Parallel()
	_, keyPEM := testRSAKey(t)
	_, p8 := testP8Key(t)
	valid := func() map[string]string {
		return map[string]string{
			"type": "service_account", "project_id": "test-project", "private_key_id": "k",
			"private_key": keyPEM, "client_email": "notify@test-project.example.invalid",
			"token_uri": "https://oauth2.example.invalid/token",
		}
	}
	tests := []struct {
		name string
		edit func(m map[string]string)
		raw  string
		want string
	}{
		{name: "not json", raw: "{", want: "not JSON"},
		{name: "wrong type", edit: func(m map[string]string) { m["type"] = "authorized_user" }, want: "service_account"},
		{name: "no project", edit: func(m map[string]string) { delete(m, "project_id") }, want: "project_id"},
		{name: "no key", edit: func(m map[string]string) { delete(m, "private_key") }, want: "private_key"},
		{name: "garbage key", edit: func(m map[string]string) { m["private_key"] = "TEST-not-a-key" }, want: "private_key"},
		{name: "ec key", edit: func(m map[string]string) { m["private_key"] = string(p8) }, want: "not an RSA key"},
		{name: "http token uri", edit: func(m map[string]string) { m["token_uri"] = "http://oauth2.example.invalid/token" }, want: "token_uri"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw := []byte(tt.raw)
			if tt.edit != nil {
				m := valid()
				tt.edit(m)
				var err error
				if raw, err = json.Marshal(m); err != nil {
					t.Fatal(err)
				}
			}
			_, err := push.ParseFCMServiceAccount(raw)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), "TEST-not-a-key") {
				t.Errorf("error text leaks key material: %v", err)
			}
		})
	}
	// The default token_uri is Google's.
	m := valid()
	delete(m, "token_uri")
	raw, _ := json.Marshal(m)
	creds, err := push.ParseFCMServiceAccount(raw)
	if err != nil || creds.TokenURI != push.DefaultGoogleTokenURL {
		t.Errorf("default token_uri: %v %q", err, creds.TokenURI)
	}
	if s := fmt.Sprintf("%v %+v %#v", creds, creds, creds); strings.Contains(s, "PRIVATE") || strings.Contains(s, "notify@") {
		t.Errorf("credentials print their contents: %s", s)
	}
}
