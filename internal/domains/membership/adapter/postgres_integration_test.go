package adapter_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/membership/adapter"
	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/staffauth"
	"birge/internal/platform/testinfra"
)

const testName = "Example Name"

// testEncryptor is a test-only stand-in for the platform crypto package: it
// binds the ciphertext to the member ID and never stores the plaintext as is.
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

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func setup(t *testing.T) (*pgxpool.Pool, *service.Service) {
	t.Helper()
	pool := testinfra.Postgres(t)
	svc := service.New(&adapter.Postgres{Pool: pool}, testEncryptor{}, fixedClock{time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)})
	return pool, svc
}

func as(identity uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: identity})
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConcurrentFirstGetMeCreatesOneMember(t *testing.T) {
	pool, svc := setup(t)
	identity := uuid.New()
	const callers = 20
	ids := make([]uuid.UUID, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Go(func() {
			<-start
			m, err := svc.Me(as(identity))
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = m.ID
		})
	}
	close(start)
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("different members returned: %v", ids)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM membership.members WHERE identity_id = $1`, identity); n != 1 {
		t.Errorf("members = %d, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM platform.outbox WHERE event_type = 'membership.MemberRegistered' AND payload = jsonb_build_object('member_id', $1::text)`, ids[0].String()); n != 1 {
		t.Errorf("MemberRegistered events = %d, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM platform.outbox`); n != 1 {
		t.Errorf("outbox rows = %d, want 1", n)
	}
}

func TestConcurrentVerificationRequestsAreIdempotent(t *testing.T) {
	pool, svc := setup(t)
	ctx := as(uuid.New())
	me, err := svc.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if m, err := svc.RequestVerification(ctx); err != nil || m.Verification != model.VerificationPending {
				t.Errorf("request: %v, %v", m.Verification, err)
			}
		})
	}
	wg.Wait()
	if n := count(t, pool, `SELECT count(*) FROM membership.verification_requests WHERE member_id = $1 AND status = 'pending'`, me.ID); n != 1 {
		t.Errorf("pending requests = %d, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM platform.outbox o JOIN membership.verification_requests r ON o.payload->>'request_id' = r.id::text
		WHERE o.event_type = 'membership.VerificationRequested' AND o.payload->>'member_id' = $1::text`, me.ID.String()); n != 1 {
		t.Errorf("VerificationRequested events = %d, want 1", n)
	}
}

func TestProfileChildrenAndReference(t *testing.T) {
	pool, svc := setup(t)
	ctx := context.Background()
	var cityA, cityB, district uuid.UUID
	err := pool.QueryRow(ctx, `WITH a AS (INSERT INTO membership.cities (code) VALUES ('city_a') RETURNING id),
		b AS (INSERT INTO membership.cities (code) VALUES ('city_b') RETURNING id),
		d AS (INSERT INTO membership.districts (city_id, code) SELECT id, 'district_a' FROM a RETURNING id)
		SELECT (SELECT id FROM a), (SELECT id FROM b), (SELECT id FROM d)`).Scan(&cityA, &cityB, &district)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO membership.needs (code) VALUES ('autism'), ('hearing')`); err != nil {
		t.Fatal(err)
	}

	cities, err := svc.Cities(ctx)
	if err != nil || len(cities) != 2 {
		t.Fatalf("cities = %+v, %v", cities, err)
	}
	if cities[0].ID != cityA || len(cities[0].Districts) != 1 || cities[0].Districts[0].ID != district ||
		cities[1].ID != cityB || cities[1].Districts == nil || len(cities[1].Districts) != 0 {
		t.Errorf("cities = %+v", cities)
	}
	if needs, err := svc.Needs(ctx); err != nil || strings.Join(needs, ",") != "autism,hearing" {
		t.Errorf("needs = %v, %v", needs, err)
	}

	me := as(uuid.New())
	lang := model.LangRU
	name := testName
	m, err := svc.UpdateMe(me, model.ProfileUpdate{DisplayName: &name, Lang: &lang, DistrictID: &district})
	if err != nil {
		t.Fatal(err)
	}
	if m.DisplayName != testName || m.Lang != model.LangRU || m.CityID == nil || *m.CityID != cityA {
		t.Errorf("member = %+v", m)
	}
	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT display_name FROM membership.members WHERE id = $1`, m.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(testName)) {
		t.Error("display name stored in plaintext")
	}
	if n := count(t, pool, `SELECT count(*) FROM platform.outbox WHERE payload::text LIKE '%' || $1 || '%'`, testName); n != 0 {
		t.Error("display name in an event")
	}
	unknown := uuid.New()
	if _, err := svc.UpdateMe(me, model.ProfileUpdate{DistrictID: &unknown}); !errors.Is(err, model.ErrUnknownDistrict) {
		t.Errorf("unknown district: %v", err)
	}

	key := uuid.New()
	c, err := svc.AddChild(me, key, model.ChildInput{BirthYear: 2019, Needs: []string{"hearing", "autism"}})
	if err != nil {
		t.Fatal(err)
	}
	if again, err := svc.AddChild(me, key, model.ChildInput{BirthYear: 2019}); err != nil || again.ID != c.ID {
		t.Fatalf("replay = %v, %v", again.ID, err)
	}
	if _, err := svc.AddChild(me, uuid.New(), model.ChildInput{BirthYear: 2020, Needs: []string{"unknown"}}); !errors.Is(err, model.ErrUnknownNeed) {
		t.Errorf("unknown need: %v", err)
	}
	if _, err := svc.AddChild(me, uuid.New(), model.ChildInput{BirthYear: 2021}); err != nil {
		t.Fatal(err)
	}
	upd, err := svc.UpdateChild(me, c.ID, model.ChildInput{BirthYear: 2018, Needs: []string{"autism"}})
	if err != nil || upd.BirthYear != 2018 || strings.Join(upd.Needs, ",") != "autism" {
		t.Fatalf("update = %+v, %v", upd, err)
	}
	other := as(uuid.New())
	if _, err := svc.UpdateChild(other, c.ID, model.ChildInput{BirthYear: 2018}); !errors.Is(err, model.ErrChildNotFound) {
		t.Errorf("other member's child: %v", err)
	}
	if err := svc.DeleteChild(other, c.ID); !errors.Is(err, model.ErrChildNotFound) {
		t.Errorf("delete other member's child: %v", err)
	}
	got, err := svc.Me(me)
	if err != nil || len(got.Children) != 2 || got.Children[0].ID != c.ID || got.Children[1].Needs == nil {
		t.Fatalf("children = %+v, %v", got.Children, err)
	}
	if err := svc.DeleteChild(me, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteChild(me, c.ID); !errors.Is(err, model.ErrChildNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestBlocks(t *testing.T) {
	pool, svc := setup(t)
	ctx := context.Background()
	aCtx, bCtx := as(uuid.New()), as(uuid.New())
	a, err := svc.Me(aCtx)
	if err != nil {
		t.Fatal(err)
	}
	name := testName
	b, err := svc.UpdateMe(bCtx, model.ProfileUpdate{DisplayName: &name})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Block(aCtx, a.ID); !errors.Is(err, model.ErrSelfBlock) {
		t.Errorf("self block: %v", err)
	}
	if err := svc.Block(aCtx, uuid.New()); !errors.Is(err, model.ErrMemberNotFound) {
		t.Errorf("unknown member: %v", err)
	}
	for range 2 {
		if err := svc.Block(aCtx, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM membership.blocks`); n != 1 {
		t.Errorf("blocks = %d, want 1", n)
	}
	for _, pair := range [][2]uuid.UUID{{a.ID, b.ID}, {b.ID, a.ID}} {
		if blocked, err := svc.IsBlocked(ctx, pair[0], pair[1]); err != nil || !blocked {
			t.Errorf("IsBlocked(%v, %v) = %v, %v", pair[0], pair[1], blocked, err)
		}
	}
	list, err := svc.Blocks(aCtx)
	if err != nil || len(list) != 1 || list[0].MemberID != b.ID || list[0].DisplayName != testName {
		t.Fatalf("blocks = %+v, %v", list, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO membership.blocks (blocker, blocked) VALUES ($1, $1)`, a.ID); err == nil {
		t.Error("database accepted a self-block")
	}
	for range 2 {
		if err := svc.Unblock(aCtx, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	if blocked, err := svc.IsBlocked(ctx, b.ID, a.ID); err != nil || blocked {
		t.Errorf("after unblock: %v, %v", blocked, err)
	}

	m, err := svc.Member(ctx, b.ID)
	if err != nil || m.DisplayName != testName {
		t.Errorf("Member = %+v, %v", m, err)
	}
	if _, err := svc.Member(ctx, uuid.New()); !errors.Is(err, model.ErrMemberNotFound) {
		t.Errorf("unknown Member: %v", err)
	}
}

func insertTestMember(t *testing.T, pool *pgxpool.Pool, name string, districtID *uuid.UUID, verification string, verifiedAt *time.Time, suspendedAt *time.Time, reason string, deletedAt *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	identityID := uuid.New()
	var sealed []byte
	if name != "" {
		var err error
		sealed, err = testEncryptor{}.Seal(context.Background(), id, []byte(name))
		if err != nil {
			t.Fatal(err)
		}
	}
	var rReason *string
	if reason != "" {
		rReason = &reason
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO membership.members (
			id, identity_id, display_name, district_id, verification, verified_at,
			suspended_at, suspended_reason, deleted_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())`,
		id, identityID, sealed, districtID, verification, verifiedAt,
		suspendedAt, rReason, deletedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStaffListMembersIntegration(t *testing.T) {
	pool, svc := setup(t)
	ctx := context.Background()

	// Set up reference cities and districts
	var cityA, distA, distB uuid.UUID
	err := pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO membership.cities (code) VALUES ('city_staff_test') RETURNING id),
		     da AS (INSERT INTO membership.districts (city_id, code) SELECT id, 'dist_a' FROM c RETURNING id),
		     db AS (INSERT INTO membership.districts (city_id, code) SELECT id, 'dist_b' FROM c RETURNING id)
		SELECT (SELECT id FROM c), (SELECT id FROM da), (SELECT id FROM db)`).Scan(&cityA, &distA, &distB)
	if err != nil {
		t.Fatal(err)
	}

	staffID := uuid.New()
	staffCtx := auth.WithPrincipal(ctx, auth.Principal{
		IdentityID: staffID,
		Staff:      true,
		MFA:        true,
		Roles:      []string{staffauth.RoleModerator},
	})

	baseTime := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	t1 := baseTime.Add(1 * time.Hour)
	t2 := baseTime.Add(2 * time.Hour)
	t3 := baseTime.Add(3 * time.Hour)
	t4 := baseTime.Add(4 * time.Hour)
	t5 := baseTime.Add(5 * time.Hour)

	s1 := baseTime.Add(10 * time.Hour)
	s2 := baseTime.Add(20 * time.Hour)
	s3 := baseTime.Add(30 * time.Hour)

	// m1: verified at t1, dist A
	m1 := insertTestMember(t, pool, "Member 1", &distA, "verified", &t1, nil, "", nil)
	// m2: verified at t2, dist B
	m2 := insertTestMember(t, pool, "Member 2", &distB, "verified", &t2, nil, "", nil)
	// m3: verified at t3, dist A
	m3 := insertTestMember(t, pool, "Member 3", &distA, "verified", &t3, nil, "", nil)
	// m4: verified at t4, dist A, also suspended at s1
	m4 := insertTestMember(t, pool, "Member 4", &distA, "verified", &t4, &s1, "spam", nil)
	// m5: unverified (new), dist A
	_ = insertTestMember(t, pool, "Member 5", &distA, "new", nil, nil, "", nil)
	// m6: unverified (new), dist B, suspended at s2
	m6 := insertTestMember(t, pool, "Member 6", &distB, "new", nil, &s2, "harassment", nil)
	// m7: deleted member (was verified at t5, suspended at s3, tombstoned)
	delTime := baseTime.Add(40 * time.Hour)
	_ = insertTestMember(t, pool, "Member 7", &distA, "new", &t5, &s3, "banned", &delTime)

	// 1. Test status = verified (no district filter)
	// Should return m4, m3, m2, m1 in descending order of verified_at.
	// m5 (unverified), m6 (unverified), m7 (deleted) must be absent.
	pageVerified, err := svc.StaffListMembers(staffCtx, model.StaffMemberListFilter{
		Status: model.StaffMemberStatusVerified,
	})
	if err != nil {
		t.Fatalf("StaffListMembers verified: %v", err)
	}
	if len(pageVerified.Items) != 4 {
		t.Fatalf("verified items = %d, want 4", len(pageVerified.Items))
	}
	expectedVerifiedOrder := []uuid.UUID{m4, m3, m2, m1}
	for i, wantID := range expectedVerifiedOrder {
		if pageVerified.Items[i].ID != wantID {
			t.Errorf("verified item [%d] ID = %v, want %v", i, pageVerified.Items[i].ID, wantID)
		}
		if !pageVerified.Items[i].Verified {
			t.Errorf("verified item [%d] Verified is false", i)
		}
		if pageVerified.Items[i].VerifiedAt == nil {
			t.Errorf("verified item [%d] VerifiedAt is nil", i)
		}
	}
	// Check m4 is both verified and suspended
	if !pageVerified.Items[0].Suspended || pageVerified.Items[0].SuspendedAt == nil {
		t.Errorf("m4 expected to have suspended=true and SuspendedAt set")
	}
	// Check display name decrypted
	if pageVerified.Items[0].DisplayName != "Member 4" {
		t.Errorf("m4 DisplayName = %q, want %q", pageVerified.Items[0].DisplayName, "Member 4")
	}

	// 2. Test status = verified with district filter (distA)
	// Should return m4, m3, m1 (m2 is in distB).
	pageDistA, err := svc.StaffListMembers(staffCtx, model.StaffMemberListFilter{
		Status:     model.StaffMemberStatusVerified,
		DistrictID: &distA,
	})
	if err != nil {
		t.Fatalf("StaffListMembers verified distA: %v", err)
	}
	if len(pageDistA.Items) != 3 {
		t.Fatalf("verified distA items = %d, want 3", len(pageDistA.Items))
	}
	expectedDistA := []uuid.UUID{m4, m3, m1}
	for i, wantID := range expectedDistA {
		if pageDistA.Items[i].ID != wantID {
			t.Errorf("distA item [%d] ID = %v, want %v", i, pageDistA.Items[i].ID, wantID)
		}
	}

	// 3. Test status = suspended (no district filter)
	// Should return m6 (s2), m4 (s1) in descending order of suspended_at.
	// m1, m2, m3, m5 (non-suspended) and m7 (deleted) must be absent.
	pageSuspended, err := svc.StaffListMembers(staffCtx, model.StaffMemberListFilter{
		Status: model.StaffMemberStatusSuspended,
	})
	if err != nil {
		t.Fatalf("StaffListMembers suspended: %v", err)
	}
	if len(pageSuspended.Items) != 2 {
		t.Fatalf("suspended items = %d, want 2", len(pageSuspended.Items))
	}
	expectedSuspended := []uuid.UUID{m6, m4}
	for i, wantID := range expectedSuspended {
		if pageSuspended.Items[i].ID != wantID {
			t.Errorf("suspended item [%d] ID = %v, want %v", i, pageSuspended.Items[i].ID, wantID)
		}
		if !pageSuspended.Items[i].Suspended || pageSuspended.Items[i].SuspendedAt == nil {
			t.Errorf("suspended item [%d] Suspended or SuspendedAt missing", i)
		}
	}
	// m6 is suspended but unverified
	if pageSuspended.Items[0].Verified || pageSuspended.Items[0].VerifiedAt != nil {
		t.Errorf("m6 expected to be unverified, got Verified=%v", pageSuspended.Items[0].Verified)
	}
	// m4 is suspended and verified
	if !pageSuspended.Items[1].Verified || pageSuspended.Items[1].VerifiedAt == nil {
		t.Errorf("m4 expected to be verified, got Verified=%v", pageSuspended.Items[1].Verified)
	}

	// 4. Test status = suspended with district filter (distA)
	// Should return only m4.
	pageSuspendedDistA, err := svc.StaffListMembers(staffCtx, model.StaffMemberListFilter{
		Status:     model.StaffMemberStatusSuspended,
		DistrictID: &distA,
	})
	if err != nil {
		t.Fatalf("StaffListMembers suspended distA: %v", err)
	}
	if len(pageSuspendedDistA.Items) != 1 || pageSuspendedDistA.Items[0].ID != m4 {
		t.Fatalf("suspended distA items = %+v, want [m4]", pageSuspendedDistA.Items)
	}

	// 5. Test Keyset Paging across pages (55 members > 50 page size)
	var pagingIDs []uuid.UUID
	for i := range 55 {
		vt := baseTime.Add(time.Duration(100+i) * time.Minute)
		name := fmt.Sprintf("PageMember %02d", i)
		id := insertTestMember(t, pool, name, &distB, "verified", &vt, nil, "", nil)
		pagingIDs = append(pagingIDs, id)
	}

	// Page 1: first 50
	page1, err := svc.StaffListMembers(staffCtx, model.StaffMemberListFilter{
		Status:     model.StaffMemberStatusVerified,
		DistrictID: &distB,
	})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	// distB had m2 initially, plus 55 new members = 56 total.
	// Page 1 should return exactly 50 items.
	if len(page1.Items) != model.StaffMemberPageSize {
		t.Fatalf("page 1 items = %d, want %d", len(page1.Items), model.StaffMemberPageSize)
	}
	if page1.NextCursor == "" {
		t.Fatal("page 1 expected non-empty NextCursor")
	}

	// Page 2: remaining 6
	page2, err := svc.StaffListMembers(staffCtx, model.StaffMemberListFilter{
		Status:     model.StaffMemberStatusVerified,
		DistrictID: &distB,
		Cursor:     page1.NextCursor,
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.Items) != 6 {
		t.Fatalf("page 2 items = %d, want 6", len(page2.Items))
	}
	if page2.NextCursor != "" {
		t.Fatalf("page 2 expected empty NextCursor, got %q", page2.NextCursor)
	}

	// Verify no duplicates between page 1 and page 2
	seen := make(map[uuid.UUID]bool)
	for _, it := range page1.Items {
		if seen[it.ID] {
			t.Errorf("duplicate item across pages: %v", it.ID)
		}
		seen[it.ID] = true
	}
	for _, it := range page2.Items {
		if seen[it.ID] {
			t.Errorf("duplicate item across pages: %v", it.ID)
		}
		seen[it.ID] = true
	}
	if len(seen) != 56 {
		t.Errorf("total seen = %d, want 56", len(seen))
	}
	for _, id := range pagingIDs {
		if !seen[id] {
			t.Errorf("member %v missing from the pages", id)
		}
	}

	// 6. Verify Audit Entry Written to platform.audit_log
	var auditCount int
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM platform.audit_log
		WHERE actor_id = $1
		  AND actor_role = 'staff'
		  AND action = 'membership.member.list'
		  AND object_type = 'membership.member'
		  AND reason = 'verified'`, staffID).Scan(&auditCount)
	if err != nil {
		t.Fatalf("audit count query: %v", err)
	}
	// We made 4 verified calls with staffCtx: pageVerified, pageDistA, page1, page2
	if auditCount != 4 {
		t.Errorf("audit count for verified = %d, want 4", auditCount)
	}

	var auditSuspendedCount int
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM platform.audit_log
		WHERE actor_id = $1
		  AND actor_role = 'staff'
		  AND action = 'membership.member.list'
		  AND object_type = 'membership.member'
		  AND reason = 'suspended'`, staffID).Scan(&auditSuspendedCount)
	if err != nil {
		t.Fatalf("audit suspended count query: %v", err)
	}
	// We made 2 suspended calls: pageSuspended, pageSuspendedDistA
	if auditSuspendedCount != 2 {
		t.Errorf("audit count for suspended = %d, want 2", auditSuspendedCount)
	}

	// Verify no display names logged in audit_log
	var leakCount int
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM platform.audit_log
		WHERE reason LIKE '%Member%'`).Scan(&leakCount)
	if err != nil {
		t.Fatal(err)
	}
	if leakCount != 0 {
		t.Errorf("personal data leaked in audit_log reason: count = %d", leakCount)
	}
}
