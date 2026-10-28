package staffdir

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/platform/audit"
	"birge/internal/platform/auth"
	"birge/internal/platform/staffdir/api"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// fakeStore answers from memory and remembers what it was asked.
type fakeStore struct {
	entries  []AuditEntry
	accounts map[uuid.UUID]Account
	counts   []ActionCount
	err      error

	query     AuditQuery
	read      *audit.Entry
	since     time.Time
	listLimit int
	calls     int
}

func (f *fakeStore) ReadAuditLog(_ context.Context, q AuditQuery, read audit.Entry) ([]AuditEntry, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	f.query, f.read = q, &read
	if len(f.entries) > q.Limit {
		return f.entries[:q.Limit], nil
	}
	return f.entries, nil
}

func (f *fakeStore) ListAccounts(_ context.Context, limit int) ([]Account, error) {
	f.calls++
	f.listLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	out := make([]Account, 0, len(f.accounts))
	for _, a := range f.accounts {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeStore) GetAccount(_ context.Context, id uuid.UUID) (Account, error) {
	f.calls++
	if f.err != nil {
		return Account{}, f.err
	}
	a, ok := f.accounts[id]
	if !ok {
		return Account{}, ErrNotFound
	}
	return a, nil
}

func (f *fakeStore) CountActions(_ context.Context, _ uuid.UUID, since time.Time, _ int) ([]ActionCount, error) {
	f.calls++
	f.since = since
	return f.counts, f.err
}

var (
	adminID = uuid.MustParse("0199a000-0000-7000-8000-000000000001")
	otherID = uuid.MustParse("0199a000-0000-7000-8000-000000000002")
)

func staffCtx(roles []string, mfa bool) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: adminID, Staff: true, Roles: roles, MFA: mfa})
}

func newService(st *fakeStore) (*Service, *Server) {
	svc := &Service{Store: st, Clock: fixedClock{now}}
	return svc, &Server{Service: svc}
}

// status runs one operation through Server and returns the HTTP status and
// problem code it writes.
type operation func(ctx context.Context, h *Server) (status int, code string, err error)

func visit(t *testing.T, v func(w http.ResponseWriter) error) (int, string) {
	t.Helper()
	rec := &recorder{header: http.Header{}}
	if err := v(rec); err != nil {
		t.Fatal(err)
	}
	code := ""
	if i := strings.Index(rec.body.String(), `"code":"`); i >= 0 {
		rest := rec.body.String()[i+8:]
		code = rest[:strings.IndexByte(rest, '"')]
	}
	return rec.status, code
}

type recorder struct {
	header http.Header
	status int
	body   strings.Builder
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(s int)           { r.status = s }

func operations(t *testing.T) map[string]operation {
	return map[string]operation{
		"listAuditEntries": func(ctx context.Context, h *Server) (int, string, error) {
			resp, err := h.ListAuditEntries(ctx, api.ListAuditEntriesRequestObject{})
			if err != nil {
				return 0, "", err
			}
			st, code := visit(t, resp.VisitListAuditEntriesResponse)
			return st, code, nil
		},
		"listStaffAccounts": func(ctx context.Context, h *Server) (int, string, error) {
			resp, err := h.ListStaffAccounts(ctx, api.ListStaffAccountsRequestObject{})
			if err != nil {
				return 0, "", err
			}
			st, code := visit(t, resp.VisitListStaffAccountsResponse)
			return st, code, nil
		},
		"getStaffAccount": func(ctx context.Context, h *Server) (int, string, error) {
			resp, err := h.GetStaffAccount(ctx, api.GetStaffAccountRequestObject{StaffId: adminID})
			if err != nil {
				return 0, "", err
			}
			st, code := visit(t, resp.VisitGetStaffAccountResponse)
			return st, code, nil
		},
	}
}

// Deny by default: only admin with MFA passes, for every operation, and a
// refused call never reaches the store.
func TestDenyByDefault(t *testing.T) {
	member := auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: adminID, MemberID: otherID})
	cases := []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"anonymous", context.Background(), http.StatusUnauthorized},
		{"member session", member, http.StatusUnauthorized},
		{"staff with nil id", auth.WithPrincipal(context.Background(), auth.Principal{Staff: true, Roles: []string{"admin"}, MFA: true}), http.StatusUnauthorized},
		{"psychologist", staffCtx([]string{"psychologist"}, true), http.StatusForbidden},
		{"moderator", staffCtx([]string{"moderator"}, true), http.StatusForbidden},
		{"lawyer", staffCtx([]string{"lawyer"}, true), http.StatusForbidden},
		{"no roles", staffCtx(nil, true), http.StatusForbidden},
		{"admin without mfa", staffCtx([]string{"admin"}, false), http.StatusForbidden},
		{"admin with mfa", staffCtx([]string{"admin", "moderator"}, true), http.StatusOK},
	}
	for opName, op := range operations(t) {
		for _, tc := range cases {
			t.Run(opName+"/"+tc.name, func(t *testing.T) {
				st := &fakeStore{accounts: map[uuid.UUID]Account{adminID: {ID: adminID, Roles: []string{"admin"}}}}
				_, h := newService(st)
				got, code, err := op(tc.ctx, h)
				if err != nil {
					t.Fatal(err)
				}
				if got != tc.want {
					t.Fatalf("status = %d (%s), want %d", got, code, tc.want)
				}
				if tc.want != http.StatusOK && st.calls != 0 {
					t.Fatalf("refused call reached the store (%d calls)", st.calls)
				}
				if tc.want == http.StatusForbidden && code != "forbidden" {
					t.Errorf("code = %q, want forbidden", code)
				}
			})
		}
	}
}

func entries(n int) []AuditEntry {
	out := make([]AuditEntry, n)
	for i := range out {
		out[i] = AuditEntry{
			ID: uuid.New(), OccurredAt: now.Add(-time.Duration(i) * time.Minute), ActorID: otherID,
			ActorRole: "moderator", Action: "moderation.approve", ObjectType: "moderation.item",
		}
	}
	return out
}

func TestListAuditEntries(t *testing.T) {
	ctx := staffCtx([]string{"admin"}, true)
	actor, object := uuid.New(), uuid.New()
	from, to := now.Add(-48*time.Hour), now

	t.Run("filters, page size, cursor and the read entry", func(t *testing.T) {
		st := &fakeStore{entries: entries(AuditPageSize + 5)}
		svc, _ := newService(st)
		page, err := svc.ListAuditEntries(ctx, AuditFilter{
			ActorID: actor, ObjectType: "safety.escalation", ObjectID: object, Action: "escalation.take", From: &from, To: &to,
		})
		if err != nil {
			t.Fatal(err)
		}
		q := st.query
		if q.ActorID != actor || q.ObjectType != "safety.escalation" || q.ObjectID != object || q.Action != "escalation.take" ||
			!q.From.Equal(from) || !q.To.Equal(to) || q.CursorAt != nil || q.Limit != AuditPageSize+1 {
			t.Fatalf("query = %+v", q)
		}
		if len(page.Items) != AuditPageSize || page.NextCursor == "" {
			t.Fatalf("items = %d, cursor %q", len(page.Items), page.NextCursor)
		}
		want := audit.Entry{ActorID: adminID, ActorRole: "admin", Action: AuditActionRead, ObjectType: AuditObjectLog}
		if st.read == nil || *st.read != want {
			t.Fatalf("read entry = %+v, want %+v", st.read, want)
		}

		// The cursor carries the last entry's position to the next query.
		last := page.Items[len(page.Items)-1]
		if _, err := svc.ListAuditEntries(ctx, AuditFilter{Cursor: page.NextCursor}); err != nil {
			t.Fatal(err)
		}
		if st.query.CursorAt == nil || !st.query.CursorAt.Equal(last.OccurredAt) || st.query.CursorID != last.ID {
			t.Fatalf("cursor query = %+v, want %s %s", st.query, last.OccurredAt, last.ID)
		}
	})

	t.Run("last page has no cursor", func(t *testing.T) {
		st := &fakeStore{entries: entries(3)}
		svc, _ := newService(st)
		page, err := svc.ListAuditEntries(ctx, AuditFilter{})
		if err != nil || len(page.Items) != 3 || page.NextCursor != "" {
			t.Fatalf("page = %+v, %v", page, err)
		}
	})

	t.Run("invalid input is refused before the store", func(t *testing.T) {
		later := from.Add(time.Second)
		bad := []AuditFilter{
			{From: &later, To: &from},
			{Cursor: "not base64 !"},
			{Cursor: base64.RawURLEncoding.EncodeToString([]byte("123"))},
			{Cursor: base64.RawURLEncoding.EncodeToString([]byte("x." + uuid.NewString()))},
			{Cursor: base64.RawURLEncoding.EncodeToString([]byte("123.not-a-uuid"))},
			{Cursor: strings.Repeat("A", MaxCursorLen+1)},
		}
		for i, f := range bad {
			st := &fakeStore{}
			svc, h := newService(st)
			if _, err := svc.ListAuditEntries(ctx, f); !errors.Is(err, ErrInvalid) {
				t.Errorf("case %d: err = %v, want ErrInvalid", i, err)
			}
			if st.calls != 0 {
				t.Errorf("case %d: store called", i)
			}
			resp, err := h.ListAuditEntries(ctx, api.ListAuditEntriesRequestObject{Params: api.ListAuditEntriesParams{From: f.From, To: f.To, Cursor: optional(f.Cursor)}})
			if err != nil {
				t.Fatal(err)
			}
			if st, code := visit(t, resp.VisitListAuditEntriesResponse); st != http.StatusBadRequest || code != "invalid_request" {
				t.Errorf("case %d: HTTP %d %s, want 400 invalid_request", i, st, code)
			}
		}
	})

	t.Run("store failure is an error, not a page", func(t *testing.T) {
		st := &fakeStore{err: errors.New("db down")}
		_, h := newService(st)
		if _, err := h.ListAuditEntries(ctx, api.ListAuditEntriesRequestObject{}); err == nil {
			t.Fatal("no error")
		}
	})

	t.Run("cursor round trip", func(t *testing.T) {
		at, id := time.Date(2026, 10, 28, 1, 2, 3, 456789000, time.UTC), uuid.New()
		gotAt, gotID, err := decodeCursor(encodeCursor(at, id))
		if err != nil || !gotAt.Equal(at) || gotID != id {
			t.Fatalf("round trip = %s %s %v", gotAt, gotID, err)
		}
		if len(encodeCursor(at, id)) > MaxCursorLen {
			t.Fatal("cursor longer than the contract allows")
		}
	})
}

func TestGetStaffAccount(t *testing.T) {
	ctx := staffCtx([]string{"admin"}, true)
	acc := Account{ID: otherID, DisplayName: "Dana", Roles: []string{"moderator"}, FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now}
	st := &fakeStore{
		accounts: map[uuid.UUID]Account{otherID: acc},
		counts:   []ActionCount{{"moderation.approve", 3}, {"escalation.take", 1}},
	}
	_, h := newService(st)
	resp, err := h.GetStaffAccount(ctx, api.GetStaffAccountRequestObject{StaffId: otherID})
	if err != nil {
		t.Fatal(err)
	}
	card, ok := resp.(api.GetStaffAccount200JSONResponse)
	if !ok {
		t.Fatalf("response %T", resp)
	}
	if card.Account.Id != otherID || card.Account.DisplayName == nil || *card.Account.DisplayName != "Dana" ||
		len(card.Actions30d) != 2 || card.Actions30d[0].Action != "moderation.approve" || card.Actions30d[0].Count != 3 {
		t.Fatalf("card = %+v", card)
	}
	if !st.since.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Errorf("window starts %s, want 30 days before now", st.since)
	}

	resp, err = h.GetStaffAccount(ctx, api.GetStaffAccountRequestObject{StaffId: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if st, code := visit(t, resp.VisitGetStaffAccountResponse); st != http.StatusNotFound || code != "not_found" {
		t.Fatalf("unknown ID: %d %q, want 404 not_found", st, code)
	}

	// No counts is an empty list, not null.
	st.counts = nil
	resp, _ = h.GetStaffAccount(ctx, api.GetStaffAccountRequestObject{StaffId: otherID})
	if c := resp.(api.GetStaffAccount200JSONResponse); c.Actions30d == nil {
		t.Fatal("actions30d is nil")
	}
}

func TestListStaffAccounts(t *testing.T) {
	st := &fakeStore{accounts: map[uuid.UUID]Account{otherID: {ID: otherID, Roles: []string{"lawyer"}}}}
	_, h := newService(st)
	resp, err := h.ListStaffAccounts(staffCtx([]string{"admin"}, true), api.ListStaffAccountsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	list := resp.(api.ListStaffAccounts200JSONResponse)
	if len(list.Items) != 1 || list.Items[0].DisplayName != nil || list.Items[0].Roles[0] != api.Lawyer {
		t.Fatalf("list = %+v", list)
	}
	if st.listLimit != MaxAccounts {
		t.Errorf("limit = %d, want %d", st.listLimit, MaxAccounts)
	}
}
