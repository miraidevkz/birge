package wiring

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"birge/internal/domains/membership/authgw"
	notifyservice "birge/internal/domains/notify/service"
	"birge/internal/domains/notify/sms/guard"
	"birge/internal/platform/clock"
)

const (
	hookSecret = "courier-hook-secret-0123456789abcdef0123"
	hookPhone  = "+77001234567"
	hookCode   = "482913"
)

// The courier hook end to end through the wiring: secret, notify's sign-in
// guard and the development log sender. The notify service has no store, so a
// path that tried to persist the code would panic.
func TestInternalHandlerCourierHook(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	notify := &notifyservice.Service{
		SMS:         LogSMSSender{Logger: logger},
		SignInGuard: guard.New(guard.Config{AllowedPrefixes: []string{"+7700"}, PerNumberLimit: 1}),
		Clock:       clock.System{},
		Logger:      logger,
	}
	cfg := Config{CourierSecret: []byte(hookSecret), SMSPrefixes: []string{"+7700"}}
	h, err := InternalHandler(cfg, notify, logger)
	if err != nil {
		t.Fatal(err)
	}
	post := func(auth, path string) int {
		body := fmt.Sprintf(`{"to":%q,"template":%q,"code":%q}`, hookPhone, authgw.TemplateLoginCode, hookCode)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := post("", authgw.CourierPath); got != http.StatusUnauthorized {
		t.Fatalf("no secret: %d", got)
	}
	if got := post("Bearer wrong", authgw.CourierPath); got != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", got)
	}
	if got := post("Bearer "+hookSecret, "/v1/me"); got != http.StatusNotFound {
		t.Fatalf("other path on the internal listener: %d", got)
	}
	if got := post("Bearer "+hookSecret, authgw.CourierPath); got != http.StatusNoContent {
		t.Fatalf("first code: %d", got)
	}
	if !strings.Contains(logs.String(), `"channel":"sms"`) {
		t.Fatal("the log sender was not reached")
	}
	// The per-number limit of the sign-in guard is used up: Kratos must see a failure.
	if got := post("Bearer "+hookSecret, authgw.CourierPath); got != http.StatusTooManyRequests {
		t.Fatalf("guard refusal: %d, want 429", got)
	}
	for _, secret := range []string{hookCode, hookPhone, "7001234567", hookSecret} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("log contains %q: %s", secret, logs.String())
		}
	}
}

func TestLoadInternalListener(t *testing.T) {
	env := validEnv(t)
	env["BIRGE_INTERNAL_ADDR"] = ":8090"
	env["BIRGE_COURIER_SECRET_FILE"] = "/keys/courier"
	files := validFiles(t)
	files["/keys/courier"] = append(randomKey(t), '\n')
	cfg, err := load(env, files, CommandWorker)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InternalAddr != ":8090" || len(cfg.CourierSecret) != 64 {
		t.Fatalf("internal listener not loaded: addr %q, secret %d bytes", cfg.InternalAddr, len(cfg.CourierSecret))
	}
	// The API never opens the internal listener.
	cfg, err = load(env, files, CommandAPI)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InternalAddr != "" || cfg.CourierSecret != nil {
		t.Fatal("API loaded the internal listener")
	}
}
