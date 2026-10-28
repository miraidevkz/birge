package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/port"
	"birge/internal/domains/analytics/service"
	"birge/internal/platform/auth"
)

func TestProblemMapping(t *testing.T) {
	for err, want := range map[error]int{
		model.ErrUnauthorized: http.StatusUnauthorized,
		model.ErrForbidden:    http.StatusForbidden,
		model.ErrInvalidInput: http.StatusBadRequest,
	} {
		st, _, _, ok := problemFor(fmt.Errorf("wrapped: %w", err))
		if !ok || st != want {
			t.Errorf("%v: got %d %v, want %d", err, st, ok, want)
		}
	}
	if _, _, _, ok := problemFor(fmt.Errorf("db down")); ok {
		t.Error("unexpected mapping for an internal error")
	}
}

// emptyStore answers every query with nothing; reports are all suppressed.
type emptyStore struct{}

func (emptyStore) InTx(_ context.Context, fn func(port.Tx) error) error { return fn(emptyTx{}) }

type emptyTx struct{}

func (emptyTx) MarkProcessed(context.Context, string, uuid.UUID) (bool, error) { return true, nil }
func (emptyTx) InsertFact(context.Context, model.Fact) error                   { return nil }
func (emptyTx) CountFacts(context.Context, time.Time, time.Time, []string) (map[string]port.FactCounts, error) {
	return nil, nil
}
func (emptyTx) ActiveMembers(context.Context, string, string, time.Time, time.Time) (map[time.Time]int, error) {
	return nil, nil
}
func (emptyTx) Retention(context.Context, time.Time, time.Time, time.Time) (port.RetentionCounts, error) {
	return port.RetentionCounts{}, nil
}
func (emptyTx) SosOutcomes(context.Context, time.Time, time.Time) (port.SosCounts, error) {
	return port.SosCounts{}, nil
}
func (emptyTx) EscalationOutcomes(context.Context, time.Time, time.Time, time.Duration) (port.EscalationCounts, error) {
	return port.EscalationCounts{}, nil
}
func (emptyTx) RightsOutcomes(context.Context, time.Time, time.Time) (port.RightsCounts, error) {
	return port.RightsCounts{}, nil
}
func (emptyTx) RollUpAndDelete(context.Context, string, time.Time, time.Time) (int64, error) {
	return 0, nil
}
func (emptyTx) ScheduleRetention(context.Context, time.Time) error { return nil }
func (emptyTx) Audit(context.Context, port.AuditEntry) error       { return nil }

type clock struct{}

func (clock) Now() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }

func serve(t *testing.T, p *auth.Principal, target string) *httptest.ResponseRecorder {
	t.Helper()
	svc := &service.Service{Store: emptyStore{}, Clock: clock{}, Settings: service.Settings{TimeZone: "Asia/Almaty"}}
	h := Handler(NewStrictHandler(&Server{Service: svc}, nil))
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if p != nil {
		req = req.WithContext(auth.WithPrincipal(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTP(t *testing.T) {
	admin := &auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"admin"}, MFA: true}
	moderator := &auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"moderator"}, MFA: true}

	rec := serve(t, admin, "/staff/metrics?from=2026-03-01&to=2026-03-31")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("json: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["k"] != float64(5) || body["from"] != "2026-03-01" || body["to"] != "2026-03-31" {
		t.Fatalf("body: %s", rec.Body)
	}
	// A suppressed cell carries suppressed only, never members or value.
	if !strings.Contains(rec.Body.String(), `"raised":{"suppressed":true}`) {
		t.Fatalf("suppressed cell shape: %s", rec.Body)
	}

	rec = serve(t, admin, "/staff/metrics.csv?from=2026-03-01&to=2026-03-31")
	if rec.Code != http.StatusOK {
		t.Fatalf("csv: %d %s", rec.Code, rec.Body)
	}
	for h, want := range map[string]string{
		"Content-Type":        "text/csv",
		"Cache-Control":       "no-store",
		"Content-Disposition": `attachment; filename="birge-metrics-2026-03-01-2026-03-31.csv"`,
	} {
		if got := rec.Header().Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	if !strings.HasPrefix(rec.Body.String(), "metric,bucket,members,value,suppressed\n") {
		t.Fatalf("csv body: %s", rec.Body)
	}

	for _, tc := range []struct {
		name   string
		p      *auth.Principal
		target string
		want   int
	}{
		{"anonymous", nil, "/staff/metrics?from=2026-03-01&to=2026-03-31", http.StatusUnauthorized},
		{"moderator", moderator, "/staff/metrics?from=2026-03-01&to=2026-03-31", http.StatusForbidden},
		{"moderator csv", moderator, "/staff/metrics.csv?from=2026-03-01&to=2026-03-31", http.StatusForbidden},
		{"13 months", admin, "/staff/metrics?from=2026-01-01&to=2027-01-02", http.StatusBadRequest},
		{"13 months csv", admin, "/staff/metrics.csv?from=2026-01-01&to=2027-01-02", http.StatusBadRequest},
		{"reversed", admin, "/staff/metrics?from=2026-03-31&to=2026-03-01", http.StatusBadRequest},
	} {
		if rec := serve(t, tc.p, tc.target); rec.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
	}
}
