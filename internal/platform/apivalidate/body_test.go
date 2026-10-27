package apivalidate_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"birge/contracts/openapi"
	"birge/internal/platform/apivalidate"
)

// TestValidRequestBodyReachesHandler guards against the validator consuming
// the body: the handler must receive exactly what the client sent.
func TestValidRequestBodyReachesHandler(t *testing.T) {
	v, err := apivalidate.New(openapi.Spec, apivalidate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	const body = `{"birthYear":2018,"needs":["autism"]}`
	var got string
	h := v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		got = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/me/children", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "01890a5d-ac96-774b-bcce-b302099a8057")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if got != body {
		t.Fatalf("handler got body %q, want %q", got, body)
	}
}
