package httpserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec
}

func TestLivenessAndReadiness(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	if rec := get(Liveness(), "/healthz"); rec.Code != http.StatusNoContent {
		t.Fatalf("liveness = %d", rec.Code)
	}
	ok := Check{Name: "database", Fn: func(context.Context) error { return nil }}
	if rec := get(Readiness(logger, ok), "/readyz"); rec.Code != http.StatusNoContent {
		t.Fatalf("ready = %d", rec.Code)
	}
	down := Check{Name: "nats", Fn: func(context.Context) error { return errors.New("not connected") }}
	rec := get(Readiness(logger, ok, down), "/readyz")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "nats") {
		t.Fatalf("not ready = %d %s (the body must not name the dependency)", rec.Code, rec.Body)
	}
	if !strings.Contains(logs.String(), "dependency=nats") {
		t.Errorf("log = %s", logs.String())
	}
}

func TestLogRequestsUsesRoutePatterns(t *testing.T) {
	var logs bytes.Buffer
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/members/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	root := http.NewServeMux()
	root.Handle("/v1/", api)
	h := LogRequests(slog.New(slog.NewTextHandler(&logs, nil)), root, api)
	get(h, "/v1/members/secret-id?q=private")
	get(h, "/nowhere")
	out := logs.String()
	if !strings.Contains(out, `route="GET /v1/members/{id}"`) || !strings.Contains(out, "status=200") || !strings.Contains(out, "bytes=2") {
		t.Errorf("log = %s", out)
	}
	if strings.Contains(out, "secret-id") || strings.Contains(out, "private") {
		t.Errorf("log carries the path or query: %s", out)
	}
	if !strings.Contains(out, "status=404") {
		t.Errorf("unmatched request not logged: %s", out)
	}
}
