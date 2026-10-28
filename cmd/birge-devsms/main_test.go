package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeGateway struct {
	mu    sync.Mutex
	calls []map[string]any
	auth  []string
	reply string
}

func (f *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.calls = append(f.calls, body)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.mu.Unlock()
	_, _ = io.WriteString(w, f.reply)
}

func newTestServer(t *testing.T, telegram bool, reply string) (http.Handler, *fakeGateway) {
	t.Helper()
	fg := &fakeGateway{reply: reply}
	s := &server{log: slog.New(slog.NewTextHandler(io.Discard, nil)), prefixes: []string{"+77"}}
	if telegram {
		ts := httptest.NewServer(fg)
		t.Cleanup(ts.Close)
		s.gw = &gateway{url: ts.URL, token: "test-token", client: &http.Client{Timeout: 5 * time.Second}}
	}
	return newMux(s), fg
}

func post(t *testing.T, h http.Handler, body string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sms", strings.NewReader(body)))
	return rec.Code
}

func TestSendsKratosCodeThroughGateway(t *testing.T) {
	h, fg := newTestServer(t, true, `{"ok":true,"result":{"request_id":"r1"}}`)
	if code := post(t, h, `{"recipient":"+77000000000","body":"Birge: 123456"}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if len(fg.calls) != 1 {
		t.Fatalf("gateway calls = %d, want 1", len(fg.calls))
	}
	c := fg.calls[0]
	if c["phone_number"] != "+77000000000" || c["code"] != "123456" {
		t.Fatalf("gateway body = %v", c)
	}
	if fg.auth[0] != "Bearer test-token" {
		t.Fatalf("auth header = %q", fg.auth[0])
	}
}

func TestDropsWithoutCalling(t *testing.T) {
	cases := []struct{ name, body string }{
		{"bad json", `{`},
		{"not e164", `{"recipient":"87000000000","body":"Birge: 123456"}`},
		{"prefix not allowed", `{"recipient":"+12025550100","body":"Birge: 123456"}`},
		{"no code", `{"recipient":"+77000000000","body":"Birge"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, fg := newTestServer(t, true, `{"ok":true}`)
			if code := post(t, h, tc.body); code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if len(fg.calls) != 0 {
				t.Fatalf("gateway called %d times", len(fg.calls))
			}
		})
	}
}

func TestTelegramOffOnlyAccepts(t *testing.T) {
	h, _ := newTestServer(t, false, "")
	if code := post(t, h, `{"recipient":"+77000000000","body":"Birge: 123456"}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
}

func TestGatewayErrorStillAnswers200(t *testing.T) {
	// Kratos would retry a non-2xx; each retry would be another paid call.
	for _, reply := range []string{`{"ok":false,"error":"PHONE_NUMBER_INVALID"}`, `not json`} {
		h, fg := newTestServer(t, true, reply)
		if code := post(t, h, `{"recipient":"+77000000000","body":"Birge: 123456"}`); code != http.StatusOK {
			t.Fatalf("reply %q: status = %d", reply, code)
		}
		if len(fg.calls) != 1 {
			t.Fatalf("reply %q: calls = %d", reply, len(fg.calls))
		}
	}
}

func TestPrefixesAndTail(t *testing.T) {
	if got := prefixesFrom(" +77, +79 ,"); len(got) != 2 || got[1] != "+79" {
		t.Fatalf("prefixes = %v", got)
	}
	if got := prefixesFrom(""); len(got) != 1 || got[0] != "+77" {
		t.Fatalf("default prefixes = %v", got)
	}
	if tail("+77000000012") != "12" || tail("") != "" {
		t.Fatal("tail")
	}
}

func TestDemoCodesOnlyForTheDemoRange(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	fg := &fakeGateway{reply: `{"ok":true}`}
	ts := httptest.NewServer(fg)
	t.Cleanup(ts.Close)
	s := &server{
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		prefixes: []string{"+77"},
		gw:       &gateway{url: ts.URL, token: "t", client: &http.Client{Timeout: 5 * time.Second}},
		demo:     &demoCodes{prefix: "+770000000", now: func() time.Time { return now }, codes: map[string]demoCode{}},
	}
	h := newMux(s)
	get := func(phone string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/demo/code?phone="+strings.ReplaceAll(phone, "+", "%2B"), nil))
		return rec.Code, rec.Body.String()
	}

	post(t, h, `{"recipient":"+77000000012","body":"Birge: 482913"}`)
	if code, body := get("+77000000012"); code != http.StatusOK || !strings.Contains(body, `"482913"`) {
		t.Fatalf("demo number: %d %s", code, body)
	}
	// Served once: a later sign-in never sees the used code.
	if code, _ := get("+77000000012"); code != http.StatusNotFound {
		t.Fatalf("code served twice: %d", code)
	}
	// Demo numbers are synthetic: never sent to Telegram.
	if len(fg.calls) != 0 {
		t.Fatalf("gateway called for a demo number")
	}
	// A real number's code is never readable, even after it was sent.
	post(t, h, `{"recipient":"+77011234567","body":"Birge: 111222"}`)
	if code, _ := get("+77011234567"); code != http.StatusNotFound {
		t.Fatalf("real number readable: %d", code)
	}
	if code, _ := get("not-a-phone"); code != http.StatusNotFound {
		t.Fatalf("bad phone: %d", code)
	}
	// Codes expire.
	post(t, h, `{"recipient":"+77000000012","body":"Birge: 482914"}`)
	now = now.Add(demoTTL + time.Second)
	if code, _ := get("+77000000012"); code != http.StatusNotFound {
		t.Fatalf("expired code readable: %d", code)
	}
}

func TestNoDemoPrefixServesNothing(t *testing.T) {
	h, _ := newTestServer(t, false, "")
	post(t, h, `{"recipient":"+77000000012","body":"Birge: 482913"}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/demo/code?phone=%2B77000000012", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
