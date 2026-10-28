package httpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"birge/internal/platform/telemetry"
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

func TestObserveUsesRoutePatterns(t *testing.T) {
	var logs bytes.Buffer
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/members/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	root := http.NewServeMux()
	root.Handle("/v1/", api)
	h := Observe(slog.New(slog.NewTextHandler(&logs, nil)), root, api)
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

func TestObserveEmitsSpanAndMetrics(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := telemetry.NewTracerProvider(sdktrace.NewSimpleSpanProcessor(exp), "birge-test")
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev); _ = tp.Shutdown(context.Background()) })

	api := http.NewServeMux()
	var childOf trace.SpanContext
	api.HandleFunc("GET /v1/trusted/plans/{ownerId}", func(w http.ResponseWriter, r *http.Request) {
		// The handler's context carries the server span: database spans
		// started here become its children.
		childOf = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusTeapot)
	})
	root := http.NewServeMux()
	root.Handle("/v1/", api)
	h := Observe(slog.New(slog.NewTextHandler(io.Discard, nil)), root, api)
	before := requestCount(t, "GET /v1/trusted/plans/{ownerId}", "418")
	get(h, "/v1/trusted/plans/0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b?token=secret-token")

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	s := spans[0]
	if s.Name != "GET /v1/trusted/plans/{ownerId}" || s.SpanKind != trace.SpanKindServer {
		t.Errorf("span %q kind %v", s.Name, s.SpanKind)
	}
	if !childOf.IsValid() || childOf.SpanID() != s.SpanContext.SpanID() {
		t.Error("handler context does not carry the server span")
	}
	attrs := map[string]string{}
	for _, kv := range s.Attributes {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	if attrs[telemetry.KeyHTTPRoute] != "GET /v1/trusted/plans/{ownerId}" || attrs[telemetry.KeyHTTPStatus] != "418" || attrs[telemetry.KeyHTTPMethod] != "GET" {
		t.Errorf("attributes = %v", attrs)
	}
	for _, v := range attrs {
		if strings.Contains(v, "0193a1b2") || strings.Contains(v, "secret-token") {
			t.Errorf("span carries the raw path or query: %v", attrs)
		}
	}
	if got := requestCount(t, "GET /v1/trusted/plans/{ownerId}", "418"); got != before+1 {
		t.Errorf("birge_http_requests_total = %v, want %v", got, before+1)
	}
}

// requestCount reads birge_http_requests_total{route, status} from the
// telemetry registry.
func requestCount(t *testing.T, route, status string) float64 {
	t.Helper()
	families, err := telemetry.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != "birge_http_requests_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["route"] == route && labels["status"] == status {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}
