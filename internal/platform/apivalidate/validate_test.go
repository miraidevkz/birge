package apivalidate_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"birge/contracts/openapi"
	"birge/internal/platform/apivalidate"
	"birge/internal/platform/httpapi"
)

// Small contract covering the failure modes the middleware must enforce.
const testSpec = `
openapi: 3.1.0
info:
  title: Test API
  version: 0.0.1
servers:
  - url: /v1
paths:
  /items:
    post:
      operationId: createItem
      parameters:
        - name: X-Request-Code
          in: header
          required: true
          schema:
            type: string
            pattern: '^[A-Z]{3}$'
            maxLength: 3
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              additionalProperties: false
              required: [name]
              properties:
                name:
                  type: string
                  maxLength: 40
      responses:
        '201':
          description: Created
  /items/{itemId}:
    get:
      operationId: getItem
      parameters:
        - name: itemId
          in: path
          required: true
          schema:
            type: string
            format: uuid
      responses:
        '200':
          description: OK
`

func newTestValidator(t *testing.T, spec []byte, opts apivalidate.Options) *apivalidate.Validator {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	v, err := apivalidate.New(spec, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) httpapi.Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json; body %s", ct, rec.Body.String())
	}
	var p httpapi.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v; body %s", err, rec.Body.String())
	}
	return p
}

func TestMiddleware_testSpec(t *testing.T) {
	v := newTestValidator(t, []byte(testSpec), apivalidate.Options{})
	h := v.Middleware(okHandler())

	tests := []struct {
		name       string
		method     string
		path       string
		headers    map[string]string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:   "valid request passes",
			method: http.MethodPost,
			path:   "/v1/items",
			headers: map[string]string{
				"Content-Type":   "application/json",
				"X-Request-Code": "ABC",
			},
			body:       `{"name":"widget"}`,
			wantStatus: http.StatusNoContent,
		},
		{
			name:   "unknown body field rejected",
			method: http.MethodPost,
			path:   "/v1/items",
			headers: map[string]string{
				"Content-Type":   "application/json",
				"X-Request-Code": "ABC",
			},
			body:       `{"name":"widget","extra":true}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:   "pattern violation",
			method: http.MethodPost,
			path:   "/v1/items",
			headers: map[string]string{
				"Content-Type":   "application/json",
				"X-Request-Code": "ab",
			},
			body:       `{"name":"widget"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:   "missing required header",
			method: http.MethodPost,
			path:   "/v1/items",
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			body:       `{"name":"widget"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "unknown path 404",
			method:     http.MethodGet,
			path:       "/v1/no-such-route",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, body)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusNoContent {
				return
			}
			p := decodeProblem(t, rec)
			if p.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", p.Code, tt.wantCode)
			}
			if p.Status != tt.wantStatus {
				t.Errorf("problem status = %d, want %d", p.Status, tt.wantStatus)
			}
			// Never echo request values that may be personal data.
			raw := rec.Body.String()
			for _, leak := range []string{"widget", "extra", `"ab"`, "true"} {
				if tt.body != "" && strings.Contains(tt.body, leak) && strings.Contains(raw, leak) {
					t.Errorf("problem body leaked request value %q: %s", leak, raw)
				}
			}
		})
	}
}

func TestMiddleware_oversizedBody(t *testing.T) {
	const limit = 64
	v := newTestValidator(t, []byte(testSpec), apivalidate.Options{MaxBodyBytes: limit})
	h := v.Middleware(okHandler())

	body := `{"name":"` + strings.Repeat("x", limit) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/items", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Code", "ABC")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body %s", rec.Code, rec.Body.String())
	}
	p := decodeProblem(t, rec)
	if p.Code != "body_too_large" {
		t.Errorf("code = %q, want body_too_large", p.Code)
	}
}

func TestMiddleware_birgeSpec(t *testing.T) {
	v := newTestValidator(t, openapi.Spec, apivalidate.Options{})
	h := v.Middleware(okHandler())

	t.Run("valid client-config", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/meta/client-config", nil)
		req.Header.Set("X-App-Version", "1.2.3")
		req.Header.Set("X-App-Platform", "ios")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown body field on updateMe", func(t *testing.T) {
		body := `{"displayName":"Ada","secret":"nope"}`
		req := httptest.NewRequest(http.MethodPatch, "/v1/me", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "00000000-0000-7000-8000-000000000001")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
		}
		p := decodeProblem(t, rec)
		if p.Code != "invalid_request" {
			t.Errorf("code = %q, want invalid_request", p.Code)
		}
		if strings.Contains(rec.Body.String(), "nope") || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("problem leaked body field: %s", rec.Body.String())
		}
	})

	t.Run("pattern violation on app version", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/meta/client-config", nil)
		req.Header.Set("X-App-Version", "1.2")
		req.Header.Set("X-App-Platform", "ios")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "1.2") {
			t.Errorf("problem leaked header value: %s", rec.Body.String())
		}
	})

	t.Run("missing required header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/meta/client-config", nil)
		req.Header.Set("X-App-Version", "1.2.3")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
		}
		p := decodeProblem(t, rec)
		if p.Code != "invalid_request" {
			t.Errorf("code = %q, want invalid_request", p.Code)
		}
	})

	t.Run("unknown path 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/does-not-exist", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
		}
		p := decodeProblem(t, rec)
		if p.Code != "not_found" {
			t.Errorf("code = %q, want not_found", p.Code)
		}
	})

	t.Run("oversized body 413", func(t *testing.T) {
		limited := newTestValidator(t, openapi.Spec, apivalidate.Options{MaxBodyBytes: 32})
		lh := limited.Middleware(okHandler())
		body := `{"displayName":"` + strings.Repeat("x", 64) + `"}`
		req := httptest.NewRequest(http.MethodPatch, "/v1/me", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "00000000-0000-7000-8000-000000000002")
		rec := httptest.NewRecorder()
		lh.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413; body %s", rec.Code, rec.Body.String())
		}
		p := decodeProblem(t, rec)
		if p.Code != "body_too_large" {
			t.Errorf("code = %q, want body_too_large", p.Code)
		}
	})
}

func TestNew_invalidSpec(t *testing.T) {
	_, err := apivalidate.New([]byte("not: openapi"), apivalidate.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil {
		t.Fatal("expected error for invalid spec")
	}
}
