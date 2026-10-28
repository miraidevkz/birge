package staffdir_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/platform/auth"
	"birge/internal/platform/staffauth"
	"birge/internal/platform/staffdir"
	"birge/internal/platform/testinfra"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var base = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// insertEntry writes an audit row with an explicit time (audit.Record uses
// the database clock, which the time filters cannot be tested with).
func insertEntry(t *testing.T, pool *pgxpool.Pool, at time.Time, actor uuid.UUID, role, action, objectType string, objectID *uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO platform.audit_log (occurred_at, actor_id, actor_role, action, object_type, object_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		at, actor, role, action, objectType, objectID); err != nil {
		t.Fatal(err)
	}
}

func adminCtx(parent context.Context, id uuid.UUID) context.Context {
	return auth.WithPrincipal(parent, auth.Principal{IdentityID: id, Staff: true, Roles: []string{"admin"}, MFA: true})
}

func countReads(ctx context.Context, t *testing.T, pool *pgxpool.Pool, actor uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM platform.audit_log WHERE actor_id = $1 AND actor_role = 'admin'
		   AND action = 'audit.read' AND object_type = 'platform.audit_log' AND object_id IS NULL AND reason IS NULL`,
		actor).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordStaffAccount(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	st := &staffdir.PostgresStore{Pool: pool}
	id := uuid.New()

	if err := st.RecordStaffAccount(ctx, staffauth.Account{StaffID: id, DisplayName: "Dana", Roles: []string{"moderator"}, SeenAt: base}); err != nil {
		t.Fatal(err)
	}
	// Refresh: name gone at the IdP, roles changed; first seen stays.
	if err := st.RecordStaffAccount(ctx, staffauth.Account{StaffID: id, Roles: []string{"admin", "moderator"}, SeenAt: base.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// A stale write (another replica, older time) changes nothing.
	if err := st.RecordStaffAccount(ctx, staffauth.Account{StaffID: id, DisplayName: "Old", Roles: []string{"lawyer"}, SeenAt: base.Add(30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "" || len(got.Roles) != 2 || got.Roles[0] != "admin" ||
		!got.FirstSeenAt.Equal(base) || !got.LastSeenAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("account = %+v", got)
	}

	// The schema refuses a role outside the claim contract and a long name.
	if err := st.RecordStaffAccount(ctx, staffauth.Account{StaffID: uuid.New(), Roles: []string{"root"}, SeenAt: base}); err == nil {
		t.Error("unknown role stored")
	}
	long := make([]rune, 81)
	for i := range long {
		long[i] = 'a'
	}
	if err := st.RecordStaffAccount(ctx, staffauth.Account{StaffID: uuid.New(), DisplayName: string(long), Roles: nil, SeenAt: base}); err == nil {
		t.Error("81-character name stored")
	}
	if _, err := st.GetAccount(ctx, uuid.New()); !errors.Is(err, staffdir.ErrNotFound) {
		t.Errorf("unknown account: %v, want ErrNotFound", err)
	}
}

func TestStaffAuditLog(t *testing.T) {
	pool := testinfra.Postgres(t)
	st := &staffdir.PostgresStore{Pool: pool}
	now := base.Add(40 * 24 * time.Hour)
	svc := &staffdir.Service{Store: st, Clock: fixedClock{now}}
	ctx := context.Background()

	reader, mod, psy, member := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	item, esc := uuid.New(), uuid.New()
	if err := st.RecordStaffAccount(ctx, staffauth.Account{StaffID: mod, DisplayName: "Dana", Roles: []string{"moderator"}, SeenAt: now}); err != nil {
		t.Fatal(err)
	}

	// 120 moderator decisions, one per minute from base; 3 escalation takes by
	// a psychologist on day 35; one row by a context that records only
	// "staff" (listed); rows by a member and the system are never listed.
	for i := range 120 {
		insertEntry(t, pool, base.Add(time.Duration(i)*time.Minute), mod, "moderator", "moderation.approve", "moderation.item", &item)
	}
	for i := range 3 {
		insertEntry(t, pool, base.Add(35*24*time.Hour+time.Duration(i)*time.Second), psy, "psychologist", "escalation.take", "safety.escalation", &esc)
	}
	insertEntry(t, pool, now.Add(-time.Minute), member, "member", "emergency_plan.view_shared", "emergency.child_card", &item)
	insertEntry(t, pool, now.Add(-time.Minute), uuid.New(), "system", "membership.member_deleted", "member", &member)
	insertEntry(t, pool, now.Add(-time.Minute), uuid.New(), "staff", "membership.member_list", "member", nil)

	t.Run("pages across the whole log, newest first, staff only", func(t *testing.T) {
		seen := map[uuid.UUID]bool{}
		var prev time.Time
		cursor, pages := "", 0
		for {
			page, err := svc.ListAuditEntries(adminCtx(ctx, reader), staffdir.AuditFilter{Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			pages++
			for _, e := range page.Items {
				if seen[e.ID] {
					t.Fatalf("entry %s on two pages", e.ID)
				}
				seen[e.ID] = true
				if !prev.IsZero() && e.OccurredAt.After(prev) {
					t.Fatalf("not newest first: %s after %s", e.OccurredAt, prev)
				}
				prev = e.OccurredAt
				if !staffauth.RoleIsValid(e.ActorRole) && e.ActorRole != "staff" {
					t.Fatalf("non-staff entry listed: role %q action %s", e.ActorRole, e.Action)
				}
				if e.ActorID == mod && e.ActorName != "Dana" {
					t.Fatalf("actor name = %q, want joined from staff_accounts", e.ActorName)
				}
				if e.ActorID == psy && e.ActorName != "" {
					t.Fatalf("unknown account has a name %q", e.ActorName)
				}
			}
			if page.NextCursor == "" {
				break
			}
			if len(page.Items) != staffdir.AuditPageSize {
				t.Fatalf("non-last page has %d items", len(page.Items))
			}
			cursor = page.NextCursor
		}
		// 124 staff entries existed before the first read; every read adds
		// its own admin entry after selecting, so later pages may show the
		// earlier reads. All 124 must be seen and nothing else but reads.
		reads := countReads(ctx, t, pool, reader)
		if reads != pages {
			t.Fatalf("read entries = %d, want one per page (%d)", reads, pages)
		}
		if len(seen) < 124 || len(seen) > 124+pages-1 {
			t.Fatalf("listed %d entries over %d pages, want 124 plus earlier reads", len(seen), pages)
		}
	})

	t.Run("filters", func(t *testing.T) {
		from, to := base.Add(35*24*time.Hour), base.Add(35*24*time.Hour+time.Second)
		cases := []struct {
			name string
			f    staffdir.AuditFilter
			want int
		}{
			{"actor", staffdir.AuditFilter{ActorID: psy}, 3},
			{"object type", staffdir.AuditFilter{ObjectType: "safety.escalation"}, 3},
			{"object id", staffdir.AuditFilter{ObjectID: esc}, 3},
			{"action", staffdir.AuditFilter{Action: "moderation.approve", ActorID: mod}, 50},
			{"inclusive range", staffdir.AuditFilter{From: &from, To: &to}, 2},
			{"member actor never listed", staffdir.AuditFilter{ActorID: member}, 0},
			{"system rows never listed", staffdir.AuditFilter{Action: "membership.member_deleted"}, 0},
			{"no match", staffdir.AuditFilter{Action: "duty_shift.add"}, 0},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				before := countReads(ctx, t, pool, reader)
				page, err := svc.ListAuditEntries(adminCtx(ctx, reader), tc.f)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != tc.want {
					t.Fatalf("items = %d, want %d", len(page.Items), tc.want)
				}
				if countReads(ctx, t, pool, reader) != before+1 {
					t.Fatal("the read did not write its audit entry")
				}
			})
		}
	})

	t.Run("refused read writes nothing", func(t *testing.T) {
		before := countReads(ctx, t, pool, reader)
		modCtx := auth.WithPrincipal(ctx, auth.Principal{IdentityID: reader, Staff: true, Roles: []string{"moderator"}, MFA: true})
		if _, err := svc.ListAuditEntries(modCtx, staffdir.AuditFilter{}); !errors.Is(err, staffdir.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if countReads(ctx, t, pool, reader) != before {
			t.Fatal("refused read was audited as a read")
		}
	})

	t.Run("staff card counts the last 30 days", func(t *testing.T) {
		_, err := svc.GetStaffAccount(adminCtx(ctx, reader), psy)
		if !errors.Is(err, staffdir.ErrNotFound) {
			t.Fatalf("psychologist without account: %v, want ErrNotFound", err)
		}
		if err := st.RecordStaffAccount(ctx, staffauth.Account{StaffID: psy, Roles: []string{"psychologist"}, SeenAt: now}); err != nil {
			t.Fatal(err)
		}
		card, err := svc.GetStaffAccount(adminCtx(ctx, reader), psy)
		if err != nil {
			t.Fatal(err)
		}
		if len(card.Actions) != 1 || card.Actions[0].Action != "escalation.take" || card.Actions[0].Count != 3 {
			t.Fatalf("actions = %+v", card.Actions)
		}
		// The moderator's decisions are 40 days old: outside the window.
		card, err = svc.GetStaffAccount(adminCtx(ctx, reader), mod)
		if err != nil {
			t.Fatal(err)
		}
		if len(card.Actions) != 0 || card.Account.DisplayName != "Dana" {
			t.Fatalf("card = %+v", card)
		}
	})

	t.Run("accounts, most recently active first", func(t *testing.T) {
		if err := st.RecordStaffAccount(ctx, staffauth.Account{StaffID: reader, Roles: []string{"admin"}, SeenAt: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		list, err := svc.ListStaffAccounts(adminCtx(ctx, reader))
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 3 || list[0].ID != reader {
			t.Fatalf("accounts = %+v", list)
		}
	})
}
