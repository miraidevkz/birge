package adapter_test

import (
	"bytes"
	"context"
	"errors"
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
