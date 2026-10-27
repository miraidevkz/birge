package meta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newHandler() http.Handler {
	svc := &Service{Settings: Settings{
		MinVersion:    "1.2.0",
		LatestVersion: "1.4.0",
		CrisisPhones:  []CrisisPhone{{Number: "112", Label: "Emergency"}},
	}}
	return HandlerWithOptions(NewStrictHandler(svc, nil), StdHTTPServerOptions{BaseURL: "/v1"})
}

func TestGetClientConfig(t *testing.T) {
	tests := []struct {
		name, version, platform string
		wantStatus              int
		wantUpdate              bool
	}{
		{"current", "1.4.0", "ios", 200, false},
		{"minimum", "1.2.0", "android", 200, false},
		{"blocked", "1.1.9", "android", 200, true},
		{"blocked major", "0.9.0", "web", 200, true},
		{"bad version", "1.2", "ios", 400, false},
		{"bad platform", "1.4.0", "symbian", 400, false},
	}
	h := newHandler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/meta/client-config", nil)
			req.Header.Set("X-App-Version", tt.version)
			req.Header.Set("X-App-Platform", tt.platform)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus != 200 {
				return
			}
			var got ClientConfig
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.UpdateRequired != tt.wantUpdate {
				t.Errorf("updateRequired = %v, want %v", got.UpdateRequired, tt.wantUpdate)
			}
			// Crisis numbers are always present, also for blocked versions.
			if len(got.CrisisPhones) != 1 || got.CrisisPhones[0].Number != "112" {
				t.Errorf("crisis phones = %+v", got.CrisisPhones)
			}
		})
	}
}

func TestLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"1.2.3", "1.2.4", true}, {"1.10.0", "1.9.0", false}, {"2.0.0", "2.0.0", false}}
	for _, c := range cases {
		if got, err := less(c.a, c.b); err != nil || got != c.want {
			t.Errorf("less(%s, %s) = %v, %v", c.a, c.b, got, err)
		}
	}
	if _, err := less("1.2", "1.2.0"); err == nil {
		t.Error("expected error for malformed version")
	}
}
