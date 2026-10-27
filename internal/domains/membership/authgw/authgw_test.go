package authgw

import (
	"bytes"
	"context"
	"encoding/base64"
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

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// recorded is what the fake Kratos received.
type recorded struct {
	method, path, query string
	header              http.Header
	body                string
}

type fakeKratos struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []recorded
}

func newFakeKratos(t *testing.T) *fakeKratos {
	t.Helper()
	k := &fakeKratos{}
	k.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		k.mu.Lock()
		k.reqs = append(k.reqs, recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), string(b)})
		k.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(k.srv.Close)
	return k
}

func (k *fakeKratos) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.reqs)
}

func (k *fakeKratos) last() recorded {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.reqs[len(k.reqs)-1]
}

const (
	allowedNumber = "+77001234567"
	otherAllowed  = "+77019876543"
	foreignNumber = "+19005550100"
	smsCode       = "482913"
)

type harness struct {
	t      *testing.T
	kratos *fakeKratos
	clock  *fakeClock
	gw     *Gateway
	logs   *bytes.Buffer
}

func testConfig(kratosURL string) Config {
	return Config{
		KratosPublicURL: kratosURL,
		PoWKey:          bytes.Repeat([]byte("p"), 32),
		HashKey:         bytes.Repeat([]byte("h"), 32),
		PoWMaxNumber:    500,
		AllowedPrefixes: []string{"+7700", "+7701"},
		PerIP:           Rate{Burst: 100, Every: time.Second},
		PerNumber:       Rate{Burst: 100, Every: time.Second},
		Global:          Rate{Burst: 100, Every: time.Second},
	}
}

func newHarness(t *testing.T, edit func(*Config)) *harness {
	t.Helper()
	k := newFakeKratos(t)
	clk := &fakeClock{now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	logs := &bytes.Buffer{}
	cfg := testConfig(k.srv.URL)
	cfg.Clock = clk
	cfg.Logger = slog.New(slog.NewTextHandler(logs, nil))
	if edit != nil {
		edit(&cfg)
	}
	gw, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, kratos: k, clock: clk, gw: gw, logs: logs}
}

func (h *harness) do(method, target, pow, contentType, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	if pow != "" {
		req.Header.Set(PoWHeader, pow)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Cookie", "kratos_session=should-not-pass")
	req.Header.Set("Connection", "X-Hop")
	req.Header.Set("X-Hop", "1")
	rec := httptest.NewRecorder()
	h.gw.ServeHTTP(rec, req)
	return rec
}

// solve fetches a challenge and brute-forces it like the ALTCHA widget.
func (h *harness) solve() string {
	h.t.Helper()
	rec := h.do("GET", "/auth/pow", "", "", "")
	if rec.Code != 200 {
		h.t.Fatalf("pow status = %d", rec.Code)
	}
	var c challenge
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		h.t.Fatal(err)
	}
	if c.Algorithm != "SHA-256" || c.MaxNumber != 500 || !strings.Contains(c.Salt, "?expires=") {
		h.t.Fatalf("challenge = %+v", c)
	}
	for n := int64(0); n <= c.MaxNumber; n++ {
		if hashChallenge(c.Salt, n) == c.Challenge {
			return encodeSolution(solution{Algorithm: c.Algorithm, Challenge: c.Challenge, Number: n, Salt: c.Salt, Signature: c.Signature})
		}
	}
	h.t.Fatal("challenge not solvable")
	return ""
}

func encodeSolution(s solution) string {
	b, _ := json.Marshal(s)
	return base64.StdEncoding.EncodeToString(b)
}

func loginBody(number string) string {
	return `{"method":"code","identifier":"` + number + `","csrf_token":""}`
}

const jsonType = "application/json"

func TestLoginPassesWithValidPoW(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.do("POST", "/auth/self-service/login?flow=f1", h.solve(), jsonType,
		`{"method":"code","identifier":"`+allowedNumber+`","code":"`+smsCode+`"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	got := h.kratos.last()
	if got.method != "POST" || got.path != "/self-service/login" || got.query != "flow=f1" {
		t.Errorf("kratos got %s %s?%s", got.method, got.path, got.query)
	}
	for _, hdr := range []string{PoWHeader, "Cookie", "X-Hop"} {
		if got.header.Get(hdr) != "" {
			t.Errorf("header %s forwarded", hdr)
		}
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(got.body), &body); err != nil || body["identifier"] != allowedNumber || body["code"] != smsCode {
		t.Errorf("forwarded body = %s", got.body)
	}
	if strings.Contains(h.logs.String(), allowedNumber) || strings.Contains(h.logs.String(), smsCode) {
		t.Errorf("number or code logged: %s", h.logs)
	}
}

func TestRegistrationPassesWithValidPoW(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.do("POST", "/auth/self-service/registration?flow=f2", h.solve(), jsonType,
		`{"method":"code","traits":{"phone":"`+allowedNumber+`"}}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if got := h.kratos.last(); got.path != "/self-service/registration" {
		t.Errorf("kratos path = %s", got.path)
	}
}

func TestPoWRejections(t *testing.T) {
	h := newHarness(t, nil)
	good := h.solve()
	raw, _ := base64.StdEncoding.DecodeString(good)
	var s solution
	_ = json.Unmarshal(raw, &s)

	wrongNumber := s
	wrongNumber.Number = (s.Number + 1) % 501
	otherKey := &pow{key: bytes.Repeat([]byte("x"), 32), maxNumber: 500, replay: newReplayStore(10)}
	forged := s
	forged.Signature = otherKey.sign(s.Challenge)
	tooBig := s
	tooBig.Number = 501
	wrongAlg := s
	wrongAlg.Algorithm = "SHA-1"
	// A self-made challenge with a later expiry is not signed by us.
	selfMade := s
	selfMade.Salt = "00?expires=9999999999"
	selfMade.Number = 0
	selfMade.Challenge = hashChallenge(selfMade.Salt, 0)

	tests := []struct {
		name, pow string
	}{
		{"missing", ""},
		{"not base64", "%%%"},
		{"not json", base64.StdEncoding.EncodeToString([]byte("nope"))},
		{"wrong number", encodeSolution(wrongNumber)},
		{"forged signature", encodeSolution(forged)},
		{"number above max", encodeSolution(tooBig)},
		{"wrong algorithm", encodeSolution(wrongAlg)},
		{"self-made challenge", encodeSolution(selfMade)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := h.do("POST", "/auth/self-service/login?flow=f", tt.pow, jsonType, loginBody(allowedNumber))
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "pow_required") {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
		})
	}
	if n := h.kratos.count(); n != 0 {
		t.Fatalf("kratos reached %d times", n)
	}
}

func TestPoWReplayed(t *testing.T) {
	h := newHarness(t, nil)
	sol := h.solve()
	if rec := h.do("POST", "/auth/self-service/login?flow=f", sol, jsonType, loginBody(allowedNumber)); rec.Code != 200 {
		t.Fatalf("first use: status = %d: %s", rec.Code, rec.Body)
	}
	rec := h.do("POST", "/auth/self-service/login?flow=f", sol, jsonType, loginBody(allowedNumber))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("replay: status = %d, want 403", rec.Code)
	}
	if n := h.kratos.count(); n != 1 {
		t.Fatalf("kratos reached %d times, want 1", n)
	}
}

func TestPoWExpired(t *testing.T) {
	h := newHarness(t, nil)
	sol := h.solve()
	h.clock.advance(powExpiry + time.Second)
	rec := h.do("POST", "/auth/self-service/login?flow=f", sol, jsonType, loginBody(allowedNumber))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if n := h.kratos.count(); n != 0 {
		t.Fatalf("kratos reached %d times", n)
	}
}

func TestNumberRejections(t *testing.T) {
	tests := []struct {
		name, path, contentType, body string
		wantStatus                    int
	}{
		{"disallowed prefix", "/auth/self-service/login", jsonType, loginBody(foreignNumber), 400},
		{"national format", "/auth/self-service/login", jsonType, loginBody("87001234567"), 400},
		{"spaces", "/auth/self-service/login", jsonType, loginBody("+7 700 123 4567"), 400},
		{"missing identifier", "/auth/self-service/login", jsonType, `{"method":"code"}`, 400},
		{"empty identifier", "/auth/self-service/login", jsonType, `{"method":"code","identifier":""}`, 400},
		{"identifier not string", "/auth/self-service/login", jsonType, `{"method":"code","identifier":77001234567}`, 400},
		{"disallowed address", "/auth/self-service/login", jsonType,
			`{"method":"code","identifier":"` + allowedNumber + `","address":"` + foreignNumber + `"}`, 400},
		{"password method", "/auth/self-service/login", jsonType,
			`{"method":"password","identifier":"` + allowedNumber + `","password":"x"}`, 400},
		{"case variant key", "/auth/self-service/login", jsonType,
			`{"method":"code","identifier":"` + allowedNumber + `","Identifier":"` + foreignNumber + `"}`, 400},
		{"duplicate key, bad last", "/auth/self-service/login", jsonType,
			`{"method":"code","identifier":"` + allowedNumber + `","identifier":"` + foreignNumber + `"}`, 400},
		{"not an object", "/auth/self-service/login", jsonType, `["code"]`, 400},
		{"registration no traits", "/auth/self-service/registration", jsonType, `{"method":"code"}`, 400},
		{"registration disallowed", "/auth/self-service/registration", jsonType,
			`{"method":"code","traits":{"phone":"` + foreignNumber + `"}}`, 400},
		{"registration case variant trait", "/auth/self-service/registration", jsonType,
			`{"method":"code","traits":{"phone":"` + allowedNumber + `","PHONE":"` + foreignNumber + `"}}`, 400},
		{"form body", "/auth/self-service/login", "application/x-www-form-urlencoded",
			"method=code&identifier=%2B19005550100", 415},
		{"too large", "/auth/self-service/login", jsonType,
			`{"method":"code","identifier":"` + allowedNumber + `","pad":"` + strings.Repeat("a", defaultMaxBody) + `"}`, 413},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, nil)
			rec := h.do("POST", tt.path+"?flow=f", h.solve(), tt.contentType, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("content type = %q", ct)
			}
			if n := h.kratos.count(); n != 0 {
				t.Fatalf("kratos reached %d times", n)
			}
			if strings.Contains(h.logs.String(), foreignNumber) || strings.Contains(h.logs.String(), allowedNumber) {
				t.Errorf("number logged: %s", h.logs)
			}
		})
	}
}

// With duplicate keys Kratos must see the value that was checked, whatever its JSON parser keeps.
func TestDuplicateKeyForwardsCheckedValue(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.do("POST", "/auth/self-service/login?flow=f", h.solve(), jsonType,
		`{"method":"code","identifier":"`+foreignNumber+`","identifier":"`+allowedNumber+`"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if body := h.kratos.last().body; strings.Contains(body, foreignNumber) || strings.Count(body, "identifier") != 1 {
		t.Fatalf("forwarded body = %s", body)
	}
}

func TestRateLimits(t *testing.T) {
	t.Run("per number", func(t *testing.T) {
		h := newHarness(t, func(c *Config) { c.PerNumber = Rate{Burst: 2, Every: time.Hour} })
		for i := 0; i < 2; i++ {
			if rec := h.do("POST", "/auth/self-service/login?flow=f", h.solve(), jsonType, loginBody(allowedNumber)); rec.Code != 200 {
				t.Fatalf("request %d: status = %d", i, rec.Code)
			}
		}
		rec := h.do("POST", "/auth/self-service/login?flow=f", h.solve(), jsonType, loginBody(allowedNumber))
		if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
			t.Fatalf("status = %d, want 429 with Retry-After", rec.Code)
		}
		if rec := h.do("POST", "/auth/self-service/login?flow=f", h.solve(), jsonType, loginBody(otherAllowed)); rec.Code != 200 {
			t.Fatalf("other number: status = %d", rec.Code)
		}
		if n := h.kratos.count(); n != 3 {
			t.Fatalf("kratos reached %d times, want 3", n)
		}
		if !strings.Contains(h.logs.String(), "number_hash=") || strings.Contains(h.logs.String(), allowedNumber) {
			t.Errorf("logs = %s", h.logs)
		}
		// The bucket refills over time.
		h.clock.advance(time.Hour)
		if rec := h.do("POST", "/auth/self-service/login?flow=f", h.solve(), jsonType, loginBody(allowedNumber)); rec.Code != 200 {
			t.Fatalf("after refill: status = %d", rec.Code)
		}
	})
	t.Run("per ip", func(t *testing.T) {
		h := newHarness(t, func(c *Config) { c.PerIP = Rate{Burst: 3, Every: time.Hour} })
		// Fetching a challenge costs a token too.
		sol1, sol2 := h.solve(), h.solve()
		if rec := h.do("POST", "/auth/self-service/login?flow=f", sol1, jsonType, loginBody(allowedNumber)); rec.Code != 200 {
			t.Fatalf("status = %d", rec.Code)
		}
		rec := h.do("POST", "/auth/self-service/login?flow=f", sol2, jsonType, loginBody(otherAllowed))
		if rec.Code != 429 {
			t.Fatalf("status = %d, want 429", rec.Code)
		}
		if n := h.kratos.count(); n != 1 {
			t.Fatalf("kratos reached %d times, want 1", n)
		}
		// Another address has its own bucket.
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/auth/pow", nil)
		req.RemoteAddr = "192.0.2.10:1234"
		rr := httptest.NewRecorder()
		h.gw.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("other ip: status = %d", rr.Code)
		}
	})
	t.Run("global", func(t *testing.T) {
		h := newHarness(t, func(c *Config) { c.Global = Rate{Burst: 1, Every: time.Hour} })
		if rec := h.do("POST", "/auth/self-service/login?flow=f", h.solve(), jsonType, loginBody(allowedNumber)); rec.Code != 200 {
			t.Fatalf("status = %d", rec.Code)
		}
		if rec := h.do("POST", "/auth/self-service/login?flow=f", h.solve(), jsonType, loginBody(otherAllowed)); rec.Code != 429 {
			t.Fatalf("status = %d, want 429", rec.Code)
		}
		if n := h.kratos.count(); n != 1 {
			t.Fatalf("kratos reached %d times, want 1", n)
		}
	})
}

func TestOnlyAllowlistedPathsReachKratos(t *testing.T) {
	h := newHarness(t, nil)
	for _, tt := range []struct{ method, path string }{
		{"GET", "/auth/admin/identities"},
		{"GET", "/auth/self-service/settings/api"},
		{"GET", "/auth/self-service/verification/api"},
		{"POST", "/auth/self-service/recovery"},
		{"PUT", "/auth/self-service/login"},
		{"GET", "/auth/self-service/login/api/../../../admin/identities"},
	} {
		if rec := h.do(tt.method, tt.path, "", "", ""); rec.Code < 300 {
			t.Errorf("%s %s: status = %d", tt.method, tt.path, rec.Code)
		}
	}
	if n := h.kratos.count(); n != 0 {
		t.Fatalf("kratos reached %d times", n)
	}
	if rec := h.do("GET", "/auth/self-service/login/api", "", "", ""); rec.Code != 200 {
		t.Fatalf("login/api: status = %d", rec.Code)
	}
	if got := h.kratos.last(); got.path != "/self-service/login/api" || got.header.Get("Cookie") != "" {
		t.Fatalf("kratos got %s cookie=%q", got.path, got.header.Get("Cookie"))
	}
}

func TestKratosDown(t *testing.T) {
	h := newHarness(t, nil)
	h.kratos.srv.Close()
	rec := h.do("POST", "/auth/self-service/login?flow=f", h.solve(), jsonType, loginBody(allowedNumber))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "auth_unavailable") {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(h.logs.String(), allowedNumber) {
		t.Fatal("number logged")
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	for name, edit := range map[string]func(*Config){
		"relative url":  func(c *Config) { c.KratosPublicURL = "kratos" },
		"short key":     func(c *Config) { c.PoWKey = []byte("short") },
		"same keys":     func(c *Config) { c.HashKey = c.PoWKey },
		"no prefixes":   func(c *Config) { c.AllowedPrefixes = nil },
		"bad prefix":    func(c *Config) { c.AllowedPrefixes = []string{"7700"} },
		"no max number": func(c *Config) { c.PoWMaxNumber = 0 },
		"no rate":       func(c *Config) { c.Global = Rate{} },
	} {
		cfg := testConfig("http://kratos.example.invalid")
		edit(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReplayStoreBounded(t *testing.T) {
	r := newReplayStore(2)
	now := time.Unix(1000, 0)
	if !r.use("a", now.Add(time.Minute), now) || !r.use("b", now.Add(time.Second), now) {
		t.Fatal("fresh keys refused")
	}
	if r.use("c", now.Add(time.Minute), now) {
		t.Fatal("full store accepted a new key")
	}
	later := now.Add(2 * time.Second) // "b" expired and is swept
	if !r.use("c", later.Add(time.Minute), later) {
		t.Fatal("expired entry not swept")
	}
	if r.use("a", later.Add(time.Minute), later) {
		t.Fatal("replay accepted")
	}
}
