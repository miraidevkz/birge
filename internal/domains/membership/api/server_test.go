package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/membership/adapter"
	"birge/internal/domains/membership/api"
	"birge/internal/domains/membership/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/testinfra"
)

const testName = "Example Name"

// testEncryptor is a test-only stand-in for the platform crypto package.
type testEncryptor struct{}

func (testEncryptor) Seal(_ context.Context, memberID uuid.UUID, plaintext []byte) ([]byte, error) {
	out := append([]byte{}, memberID[:]...)
	for _, b := range plaintext {
		out = append(out, b^0x5a)
	}
	return out, nil
}

func (testEncryptor) Open(_ context.Context, memberID uuid.UUID, sealed []byte) ([]byte, error) {
	if len(sealed) < 16 || !bytes.Equal(sealed[:16], memberID[:]) {
		return nil, errors.New("wrong key")
	}
	out := make([]byte, 0, len(sealed)-16)
	for _, b := range sealed[16:] {
		out = append(out, b^0x5a)
	}
	return out, nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }

// testServer stands in for the session middleware: X-Test-Identity becomes the principal.
type testServer struct {
	t   *testing.T
	h   http.Handler
	log *bytes.Buffer
}

func newServer(t *testing.T) (*testServer, func(sql string, args ...any)) {
	t.Helper()
	pool := testinfra.Postgres(t)
	svc := service.New(&adapter.Postgres{Pool: pool}, testEncryptor{}, fixedClock{})
	var logs bytes.Buffer
	h := api.NewHTTPHandler(svc, slog.New(slog.NewTextHandler(&logs, nil)), "/v1")
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, err := uuid.Parse(r.Header.Get("X-Test-Identity")); err == nil {
			r = r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{IdentityID: id}))
		}
		h.ServeHTTP(w, r)
	})
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	return &testServer{t: t, h: wrapped, log: &logs}, exec
}

type call struct {
	method, path, body string
	identity           uuid.UUID
	key                bool // send an Idempotency-Key
	keyValue           string
}

func (s *testServer) do(c call) (int, http.Header, []byte) {
	s.t.Helper()
	var body io.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	}
	req := httptest.NewRequestWithContext(context.Background(), c.method, "/v1"+c.path, body)
	if c.identity != uuid.Nil {
		req.Header.Set("X-Test-Identity", c.identity.String())
	}
	if c.keyValue != "" {
		req.Header.Set("Idempotency-Key", c.keyValue)
	} else if c.key {
		req.Header.Set("Idempotency-Key", uuid.New().String())
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec.Code, rec.Header(), rec.Body.Bytes()
}

func (s *testServer) json(c call, wantStatus int, out any) {
	s.t.Helper()
	status, hdr, body := s.do(c)
	if status != wantStatus {
		s.t.Fatalf("%s %s: status %d, want %d: %s", c.method, c.path, status, wantStatus, body)
	}
	if out == nil {
		return
	}
	if ct := hdr.Get("Content-Type"); ct != "application/json" {
		s.t.Fatalf("%s %s: content type %q", c.method, c.path, ct)
	}
	if err := json.Unmarshal(body, out); err != nil {
		s.t.Fatal(err)
	}
}

func TestOperations(t *testing.T) {
	s, exec := newServer(t)
	cityID, districtID := uuid.New(), uuid.New()
	exec(`INSERT INTO membership.cities (id, code) VALUES ($1, 'city_a')`, cityID)
	exec(`INSERT INTO membership.districts (id, city_id, code) VALUES ($1, $2, 'district_a')`, districtID, cityID)
	exec(`INSERT INTO membership.needs (code) VALUES ('autism'), ('hearing')`)
	me, other := uuid.New(), uuid.New()

	var cities []api.City
	s.json(call{method: http.MethodGet, path: "/reference/districts"}, 200, &cities)
	if len(cities) != 1 || cities[0].Id != cityID || len(cities[0].Districts) != 1 || cities[0].Districts[0].Id != districtID {
		t.Errorf("cities = %+v", cities)
	}
	var needs []string
	s.json(call{method: http.MethodGet, path: "/reference/needs"}, 200, &needs)
	if strings.Join(needs, ",") != "autism,hearing" {
		t.Errorf("needs = %v", needs)
	}

	var m api.Member
	s.json(call{method: http.MethodGet, path: "/me", identity: me}, 200, &m)
	if m.Verification != api.New || m.Lang != api.Kk || m.Children == nil || len(m.Roles) != 1 || m.Roles[0] != api.Mom {
		t.Errorf("new member = %+v", m)
	}
	myID := m.Id

	s.json(call{method: http.MethodPatch, path: "/me", identity: me, key: true,
		body: `{"displayName":"` + testName + `","lang":"ru","districtId":"` + districtID.String() + `"}`}, 200, &m)
	if m.DisplayName != testName || m.Lang != api.Ru || m.CityId == nil || *m.CityId != cityID || m.DistrictId == nil {
		t.Errorf("updated member = %+v", m)
	}

	key := uuid.New().String()
	var c, again api.Child
	s.json(call{method: http.MethodPost, path: "/me/children", identity: me, keyValue: key, body: `{"birthYear":2019,"needs":["autism"]}`}, 201, &c)
	s.json(call{method: http.MethodPost, path: "/me/children", identity: me, keyValue: key, body: `{"birthYear":2019,"needs":["autism"]}`}, 201, &again)
	if c.Id != again.Id || c.BirthYear != 2019 {
		t.Errorf("child = %+v, replay = %+v", c, again)
	}
	s.json(call{method: http.MethodPut, path: "/me/children/" + c.Id.String(), identity: me, body: `{"birthYear":2018,"needs":[]}`}, 200, &c)
	if c.BirthYear != 2018 || c.Needs == nil || len(c.Needs) != 0 {
		t.Errorf("updated child = %+v", c)
	}
	s.json(call{method: http.MethodGet, path: "/me", identity: me}, 200, &m)
	if len(m.Children) != 1 {
		t.Errorf("children = %+v", m.Children)
	}
	s.json(call{method: http.MethodDelete, path: "/me/children/" + c.Id.String(), identity: me}, 204, nil)

	s.json(call{method: http.MethodPost, path: "/me/verification-requests", identity: me, key: true}, 202, &m)
	if m.Verification != api.Pending {
		t.Errorf("verification = %q", m.Verification)
	}
	s.json(call{method: http.MethodPost, path: "/me/verification-requests", identity: me, key: true}, 202, &m)

	var o api.Member
	s.json(call{method: http.MethodGet, path: "/me", identity: other}, 200, &o)
	s.json(call{method: http.MethodPut, path: "/me/blocks/" + o.Id.String(), identity: me}, 204, nil)
	s.json(call{method: http.MethodPut, path: "/me/blocks/" + o.Id.String(), identity: me}, 204, nil)
	var blocks []api.BlockedMember
	s.json(call{method: http.MethodGet, path: "/me/blocks", identity: me}, 200, &blocks)
	if len(blocks) != 1 || blocks[0].MemberId != o.Id || blocks[0].DisplayName != "" {
		t.Errorf("blocks = %+v", blocks)
	}
	s.json(call{method: http.MethodGet, path: "/me/blocks", identity: other}, 200, &blocks)
	if len(blocks) != 0 {
		t.Errorf("blocked member sees blocks: %+v", blocks)
	}
	s.json(call{method: http.MethodDelete, path: "/me/blocks/" + o.Id.String(), identity: me}, 204, nil)
	s.json(call{method: http.MethodDelete, path: "/me/blocks/" + o.Id.String(), identity: me}, 204, nil)

	exec(`UPDATE membership.members SET verification = 'verified' WHERE id = $1`, myID)

	problems := []struct {
		name   string
		call   call
		status int
		code   string
	}{
		{"anonymous me", call{method: http.MethodGet, path: "/me"}, 401, "unauthenticated"},
		{"anonymous patch", call{method: http.MethodPatch, path: "/me", key: true, body: `{"lang":"ru"}`}, 401, "unauthenticated"},
		{"anonymous blocks", call{method: http.MethodGet, path: "/me/blocks"}, 401, "unauthenticated"},
		{"anonymous block", call{method: http.MethodPut, path: "/me/blocks/" + o.Id.String()}, 401, "unauthenticated"},
		{"anonymous child", call{method: http.MethodPost, path: "/me/children", key: true, body: `{"birthYear":2019,"needs":[]}`}, 401, "unauthenticated"},
		{"anonymous verification", call{method: http.MethodPost, path: "/me/verification-requests", key: true}, 401, "unauthenticated"},
		{"self block", call{method: http.MethodPut, path: "/me/blocks/" + myID.String(), identity: me}, 400, "self_block"},
		{"self unblock", call{method: http.MethodDelete, path: "/me/blocks/" + myID.String(), identity: me}, 400, "self_block"},
		{"block unknown", call{method: http.MethodPut, path: "/me/blocks/" + uuid.NewString(), identity: me}, 404, "member_not_found"},
		{"bad member id", call{method: http.MethodPut, path: "/me/blocks/not-a-uuid", identity: me}, 400, "invalid_request"},
		{"empty patch", call{method: http.MethodPatch, path: "/me", identity: me, key: true, body: `{}`}, 400, "empty_update"},
		{"malformed patch", call{method: http.MethodPatch, path: "/me", identity: me, key: true, body: `{"lang":`}, 400, "invalid_request"},
		{"missing key", call{method: http.MethodPatch, path: "/me", identity: me, body: `{"lang":"ru"}`}, 400, "invalid_request"},
		{"long name", call{method: http.MethodPatch, path: "/me", identity: me, key: true, body: `{"displayName":"` + strings.Repeat("a", 41) + `"}`}, 400, "invalid_display_name"},
		{"bad lang", call{method: http.MethodPatch, path: "/me", identity: me, key: true, body: `{"lang":"en"}`}, 400, "invalid_lang"},
		{"unknown district", call{method: http.MethodPatch, path: "/me", identity: me, key: true, body: `{"districtId":"` + uuid.NewString() + `"}`}, 400, "unknown_district"},
		{"bad year", call{method: http.MethodPost, path: "/me/children", identity: me, key: true, body: `{"birthYear":1900,"needs":[]}`}, 400, "invalid_birth_year"},
		{"unknown need", call{method: http.MethodPost, path: "/me/children", identity: me, key: true, body: `{"birthYear":2019,"needs":["unknown"]}`}, 400, "unknown_need"},
		{"missing child", call{method: http.MethodPut, path: "/me/children/" + uuid.NewString(), identity: me, body: `{"birthYear":2019,"needs":[]}`}, 404, "child_not_found"},
		{"delete missing child", call{method: http.MethodDelete, path: "/me/children/" + uuid.NewString(), identity: me}, 404, "child_not_found"},
		{"already verified", call{method: http.MethodPost, path: "/me/verification-requests", identity: me, key: true}, 409, "already_verified"},
	}
	for _, tt := range problems {
		t.Run(tt.name, func(t *testing.T) {
			status, hdr, body := s.do(tt.call)
			if status != tt.status {
				t.Fatalf("status = %d, want %d: %s", status, tt.status, body)
			}
			if ct := hdr.Get("Content-Type"); ct != "application/problem+json" {
				t.Fatalf("content type = %q", ct)
			}
			var p api.Problem
			if err := json.Unmarshal(body, &p); err != nil {
				t.Fatal(err)
			}
			if p.Status != tt.status || p.Code == nil || *p.Code != tt.code || p.Title == "" || p.Type == "" {
				t.Errorf("problem = %s", body)
			}
		})
	}

	if strings.Contains(s.log.String(), testName) {
		t.Errorf("logs carry the display name: %s", s.log.String())
	}
}
