package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/staffauth"
)

const testName = "Example Name"

type env struct {
	store *fakeStore
	enc   *fakeEncryptor
	clock *fakeClock
	svc   *service.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{store: newFakeStore(), enc: &fakeEncryptor{}, clock: &fakeClock{t: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}}
	e.svc = service.New(e.store, e.enc, e.clock)
	return e
}

func member(identity uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: identity})
}

func newMember(t *testing.T, e *env) (context.Context, model.Member) {
	t.Helper()
	ctx := member(uuid.New())
	m, err := e.svc.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, m
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("unexpected error: %v", got)
		}
		return
	}
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func ptr[T any](v T) *T { return &v }

func TestPrincipalRequired(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"anonymous", context.Background(), model.ErrUnauthenticated},
		{"nil identity", auth.WithPrincipal(context.Background(), auth.Principal{}), model.ErrUnauthenticated},
		{"staff", auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: uuid.New(), Staff: true}), model.ErrMemberSessionRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := map[string]error{}
			_, ops["me"] = e.svc.Me(tt.ctx)
			_, ops["update"] = e.svc.UpdateMe(tt.ctx, model.ProfileUpdate{Lang: ptr(model.LangRU)})
			_, ops["addChild"] = e.svc.AddChild(tt.ctx, uuid.New(), model.ChildInput{BirthYear: 2020})
			_, ops["updateChild"] = e.svc.UpdateChild(tt.ctx, uuid.New(), model.ChildInput{BirthYear: 2020})
			ops["deleteChild"] = e.svc.DeleteChild(tt.ctx, uuid.New())
			_, ops["verify"] = e.svc.RequestVerification(tt.ctx)
			ops["block"] = e.svc.Block(tt.ctx, uuid.New())
			ops["unblock"] = e.svc.Unblock(tt.ctx, uuid.New())
			_, ops["blocks"] = e.svc.Blocks(tt.ctx)
			for op, err := range ops {
				if !errors.Is(err, tt.want) {
					t.Errorf("%s: error = %v, want %v", op, err, tt.want)
				}
			}
			if n := e.store.memberCount(); n != 0 {
				t.Errorf("members created: %d", n)
			}
		})
	}
}

func TestMeCreatesMemberOnce(t *testing.T) {
	e := newEnv(t)
	ctx := member(uuid.New())
	first, err := e.svc.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || e.store.memberCount() != 1 {
		t.Fatalf("members: %v %v (count %d)", first.ID, second.ID, e.store.memberCount())
	}
	if first.Verification != model.VerificationNew || first.Lang != model.LangKK || first.DisplayName != "" ||
		len(first.Children) != 0 || len(first.Roles) != 1 || first.Roles[0] != "mom" {
		t.Errorf("new member = %+v", first)
	}
	evs := e.store.events()
	if len(evs) != 1 || evs[0].Type != model.EventMemberRegistered || evs[0].Version != 1 {
		t.Fatalf("events = %+v", evs)
	}
	b, _ := json.Marshal(evs[0].Data)
	if string(b) != `{"member_id":"`+first.ID.String()+`"}` {
		t.Errorf("payload = %s", b)
	}
}

func TestMeConcurrentFirstCalls(t *testing.T) {
	e := newEnv(t)
	ctx := member(uuid.New())
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := e.svc.Me(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n, evs := e.store.memberCount(), e.store.events(); n != 1 || len(evs) != 1 {
		t.Fatalf("members = %d, events = %d", n, len(evs))
	}
}

func TestMeFailures(t *testing.T) {
	boom := errors.New("db down")
	tests := []struct {
		name   string
		method string
	}{
		{"lookup", "MemberByIdentity"},
		{"insert", "InsertMember"},
		{"publish", "Publish"},
		{"children", "Children"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.store.fail[tt.method] = boom
			if _, err := e.svc.Me(member(uuid.New())); !errors.Is(err, boom) {
				t.Fatalf("error = %v", err)
			}
			if e.store.memberCount() != 0 || len(e.store.events()) != 0 {
				t.Error("transaction not rolled back")
			}
		})
	}
}

func TestUpdateMe(t *testing.T) {
	district, city := uuid.New(), uuid.New()
	tests := []struct {
		name     string
		update   model.ProfileUpdate
		failSeal bool
		want     error
		wantName string
	}{
		{"name trimmed", model.ProfileUpdate{DisplayName: ptr("  " + testName + " ")}, false, nil, testName},
		{"lang", model.ProfileUpdate{Lang: ptr(model.LangRU)}, false, nil, ""},
		{"district", model.ProfileUpdate{DistrictID: &district}, false, nil, ""},
		{"empty", model.ProfileUpdate{}, false, model.ErrEmptyUpdate, ""},
		{"blank name", model.ProfileUpdate{DisplayName: ptr("   ")}, false, model.ErrInvalidDisplayName, ""},
		{"long name", model.ProfileUpdate{DisplayName: ptr(strings.Repeat("ә", 41))}, false, model.ErrInvalidDisplayName, ""},
		{"control char", model.ProfileUpdate{DisplayName: ptr("a\nb")}, false, model.ErrInvalidDisplayName, ""},
		{"bad lang", model.ProfileUpdate{Lang: ptr(model.Lang("de"))}, false, model.ErrInvalidLang, ""},
		{"unknown district", model.ProfileUpdate{DistrictID: ptr(uuid.New())}, false, model.ErrUnknownDistrict, ""},
		{"seal fails", model.ProfileUpdate{DisplayName: ptr(testName)}, true, errAny, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.store.st.districts[district] = city
			ctx, _ := newMember(t, e)
			e.enc.failSeal = tt.failSeal
			m, err := e.svc.UpdateMe(ctx, tt.update)
			if errors.Is(tt.want, errAny) {
				var me *model.Error
				if err == nil || errors.As(err, &me) {
					t.Fatalf("error = %v, want an internal error", err)
				}
				return
			}
			wantErr(t, err, tt.want)
			if err != nil {
				return
			}
			if m.DisplayName != tt.wantName {
				t.Errorf("name = %q, want %q", m.DisplayName, tt.wantName)
			}
			if tt.update.Lang != nil && m.Lang != *tt.update.Lang {
				t.Errorf("lang = %q", m.Lang)
			}
			if tt.update.DistrictID != nil && (m.DistrictID == nil || *m.DistrictID != district || m.CityID == nil || *m.CityID != city) {
				t.Errorf("district = %v, city = %v", m.DistrictID, m.CityID)
			}
			if tt.wantName != "" {
				stored := e.store.st.members[m.ID].SealedName
				if strings.Contains(string(stored), tt.wantName) {
					t.Error("display name stored in plaintext")
				}
			}
		})
	}
}

var errAny = errors.New("any internal error")

func TestOpenFailureIsInternal(t *testing.T) {
	e := newEnv(t)
	ctx, _ := newMember(t, e)
	if _, err := e.svc.UpdateMe(ctx, model.ProfileUpdate{DisplayName: ptr(testName)}); err != nil {
		t.Fatal(err)
	}
	e.enc.failOpen = true
	_, err := e.svc.Me(ctx)
	var me *model.Error
	if err == nil || errors.As(err, &me) || strings.Contains(err.Error(), testName) {
		t.Fatalf("error = %v", err)
	}
}

func TestChildren(t *testing.T) {
	e := newEnv(t)
	e.store.st.needs = []string{"autism", "hearing"}
	ctx, _ := newMember(t, e)
	otherCtx, _ := newMember(t, e)

	key := uuid.New()
	c, err := e.svc.AddChild(ctx, key, model.ChildInput{BirthYear: 2019, Needs: []string{"hearing", "autism", "hearing"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Needs) != 2 || c.Needs[0] != "autism" || c.Needs[1] != "hearing" {
		t.Errorf("needs = %v", c.Needs)
	}
	again, err := e.svc.AddChild(ctx, key, model.ChildInput{BirthYear: 2019})
	if err != nil || again.ID != c.ID {
		t.Fatalf("replay = %v, %v; want child %v", again.ID, err, c.ID)
	}

	addTests := []struct {
		name string
		key  uuid.UUID
		in   model.ChildInput
		want error
	}{
		{"prefer not to say", uuid.New(), model.ChildInput{BirthYear: 2021}, nil},
		{"nil key", uuid.Nil, model.ChildInput{BirthYear: 2021}, model.ErrInvalidIdempotencyKey},
		{"too old", uuid.New(), model.ChildInput{BirthYear: 1989}, model.ErrInvalidBirthYear},
		{"too new", uuid.New(), model.ChildInput{BirthYear: 2101}, model.ErrInvalidBirthYear},
		{"bad code", uuid.New(), model.ChildInput{BirthYear: 2020, Needs: []string{"Autism"}}, model.ErrInvalidNeeds},
		{"too many needs", uuid.New(), model.ChildInput{BirthYear: 2020, Needs: strings.Split("aa,bb,cc,dd,ee,ff,gg,hh,ii,jj,kk", ",")}, model.ErrInvalidNeeds},
		{"unknown need", uuid.New(), model.ChildInput{BirthYear: 2020, Needs: []string{"unknown"}}, model.ErrUnknownNeed},
	}
	for _, tt := range addTests {
		t.Run("add "+tt.name, func(t *testing.T) {
			_, err := e.svc.AddChild(ctx, tt.key, tt.in)
			wantErr(t, err, tt.want)
		})
	}

	m, err := e.svc.Me(ctx)
	if err != nil || len(m.Children) != 2 || m.Children[0].ID != c.ID {
		t.Fatalf("children = %+v, %v", m.Children, err)
	}

	upd, err := e.svc.UpdateChild(ctx, c.ID, model.ChildInput{BirthYear: 2018, Needs: []string{}})
	if err != nil || upd.BirthYear != 2018 || len(upd.Needs) != 0 {
		t.Fatalf("update = %+v, %v", upd, err)
	}
	// A member only ever touches her own children.
	_, err = e.svc.UpdateChild(otherCtx, c.ID, model.ChildInput{BirthYear: 2018})
	wantErr(t, err, model.ErrChildNotFound)
	wantErr(t, e.svc.DeleteChild(otherCtx, c.ID), model.ErrChildNotFound)
	_, err = e.svc.UpdateChild(ctx, c.ID, model.ChildInput{BirthYear: 2018, Needs: []string{"unknown"}})
	wantErr(t, err, model.ErrUnknownNeed)

	wantErr(t, e.svc.DeleteChild(ctx, c.ID), nil)
	wantErr(t, e.svc.DeleteChild(ctx, c.ID), model.ErrChildNotFound)
}

func TestChildrenLimit(t *testing.T) {
	e := newEnv(t)
	ctx, _ := newMember(t, e)
	for i := range model.MaxChildren {
		if _, err := e.svc.AddChild(ctx, uuid.New(), model.ChildInput{BirthYear: 2000 + i}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.svc.AddChild(ctx, uuid.New(), model.ChildInput{BirthYear: 2020})
	wantErr(t, err, model.ErrChildrenLimit)
}

func TestRequestVerification(t *testing.T) {
	e := newEnv(t)
	ctx, me := newMember(t, e)
	for range 3 {
		m, err := e.svc.RequestVerification(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if m.Verification != model.VerificationPending {
			t.Fatalf("verification = %q", m.Verification)
		}
	}
	var requested int
	for _, ev := range e.store.events() {
		if ev.Type == model.EventVerificationRequested {
			requested++
			b, _ := json.Marshal(ev.Data)
			want := `{"member_id":"` + me.ID.String() + `","request_id":"` + e.store.st.pending[me.ID].String() + `"}`
			if string(b) != want {
				t.Errorf("payload = %s, want %s", b, want)
			}
		}
	}
	if requested != 1 {
		t.Fatalf("VerificationRequested events = %d, want 1", requested)
	}

	verified := e.store.st.members[me.ID]
	verified.Verification = model.VerificationVerified
	e.store.st.members[me.ID] = verified
	_, err := e.svc.RequestVerification(ctx)
	wantErr(t, err, model.ErrAlreadyVerified)
}

func TestRequestVerificationFailures(t *testing.T) {
	boom := errors.New("db down")
	for _, method := range []string{"InsertPendingVerification", "SetVerification", "Publish"} {
		t.Run(method, func(t *testing.T) {
			e := newEnv(t)
			ctx, me := newMember(t, e)
			e.store.fail[method] = boom
			if _, err := e.svc.RequestVerification(ctx); !errors.Is(err, boom) {
				t.Fatalf("error = %v", err)
			}
			if len(e.store.st.pending) != 0 || e.store.st.members[me.ID].Verification != model.VerificationNew {
				t.Error("transaction not rolled back")
			}
		})
	}
}

func TestBlocks(t *testing.T) {
	e := newEnv(t)
	ctx, me := newMember(t, e)
	otherCtx, other := newMember(t, e)
	if _, err := e.svc.UpdateMe(otherCtx, model.ProfileUpdate{DisplayName: ptr(testName)}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		target uuid.UUID
		want   error
	}{
		{"self", me.ID, model.ErrSelfBlock},
		{"unknown", uuid.New(), model.ErrMemberNotFound},
		{"other", other.ID, nil},
		{"again", other.ID, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantErr(t, e.svc.Block(ctx, tt.target), tt.want)
		})
	}

	for _, pair := range [][2]uuid.UUID{{me.ID, other.ID}, {other.ID, me.ID}} {
		blocked, err := e.svc.IsBlocked(context.Background(), pair[0], pair[1])
		if err != nil || !blocked {
			t.Errorf("IsBlocked(%v, %v) = %v, %v", pair[0], pair[1], blocked, err)
		}
	}
	if blocked, _ := e.svc.IsBlocked(context.Background(), me.ID, me.ID); blocked {
		t.Error("member blocked from herself")
	}

	list, err := e.svc.Blocks(ctx)
	if err != nil || len(list) != 1 || list[0].MemberID != other.ID || list[0].DisplayName != testName {
		t.Fatalf("blocks = %+v, %v", list, err)
	}
	if theirs, _ := e.svc.Blocks(otherCtx); len(theirs) != 0 {
		t.Errorf("blocked member sees the block: %+v", theirs)
	}

	wantErr(t, e.svc.Unblock(ctx, me.ID), model.ErrSelfBlock)
	wantErr(t, e.svc.Unblock(ctx, other.ID), nil)
	wantErr(t, e.svc.Unblock(ctx, other.ID), nil)
	if blocked, _ := e.svc.IsBlocked(context.Background(), other.ID, me.ID); blocked {
		t.Error("still blocked after unblock")
	}
}

func TestBlocksLimit(t *testing.T) {
	e := newEnv(t)
	ctx, me := newMember(t, e)
	for range model.MaxBlocks {
		id := uuid.New()
		e.store.st.members[id] = e.store.st.members[me.ID]
		e.store.st.blocks[blockKey{me.ID, id}] = e.clock.t
	}
	_, target := newMember(t, e)
	wantErr(t, e.svc.Block(ctx, target.ID), model.ErrBlocksLimit)
	if blocked, _ := e.svc.IsBlocked(context.Background(), me.ID, target.ID); blocked {
		t.Error("block over the limit was kept")
	}
}

func TestBlockFailures(t *testing.T) {
	boom := errors.New("db down")
	for _, method := range []string{"MemberByID", "LockMember", "InsertBlock"} {
		t.Run(method, func(t *testing.T) {
			e := newEnv(t)
			ctx, _ := newMember(t, e)
			_, other := newMember(t, e)
			e.store.fail[method] = boom
			if err := e.svc.Block(ctx, other.ID); !errors.Is(err, boom) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	e := newEnv(t)
	e.store.fail["IsBlocked"] = boom
	if _, err := e.svc.IsBlocked(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, boom) {
		t.Fatalf("IsBlocked error = %v", err)
	}
}

func TestListVerifiedInDistrict(t *testing.T) {
	e := newEnv(t)
	district := uuid.New()
	otherDistrict := uuid.New()
	e.store.st.districts[district] = uuid.New()
	e.store.st.districts[otherDistrict] = uuid.New()

	_, verified := newMember(t, e)
	rec := e.store.st.members[verified.ID]
	rec.DistrictID = &district
	rec.Verification = model.VerificationVerified
	e.store.st.members[verified.ID] = rec

	_, also := newMember(t, e)
	rec = e.store.st.members[also.ID]
	rec.DistrictID = &district
	rec.Verification = model.VerificationVerified
	e.store.st.members[also.ID] = rec

	_, pending := newMember(t, e)
	rec = e.store.st.members[pending.ID]
	rec.DistrictID = &district
	rec.Verification = model.VerificationPending
	e.store.st.members[pending.ID] = rec

	_, elsewhere := newMember(t, e)
	rec = e.store.st.members[elsewhere.ID]
	rec.DistrictID = &otherDistrict
	rec.Verification = model.VerificationVerified
	e.store.st.members[elsewhere.ID] = rec

	ids, err := e.svc.ListVerifiedInDistrict(context.Background(), district)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids = %v, want 2 verified in district", ids)
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen[verified.ID] || !seen[also.ID] || seen[pending.ID] || seen[elsewhere.ID] {
		t.Fatalf("ids = %v", ids)
	}
	empty, err := e.svc.ListVerifiedInDistrict(context.Background(), uuid.New())
	if err != nil || len(empty) != 0 {
		t.Fatalf("unknown district = %v, %v", empty, err)
	}
	nilIDs, err := e.svc.ListVerifiedInDistrict(context.Background(), uuid.Nil)
	if err != nil || len(nilIDs) != 0 {
		t.Fatalf("nil district = %v, %v", nilIDs, err)
	}
}

func TestMemberForOtherContexts(t *testing.T) {
	e := newEnv(t)
	ctx, me := newMember(t, e)
	if _, err := e.svc.UpdateMe(ctx, model.ProfileUpdate{DisplayName: ptr(testName)}); err != nil {
		t.Fatal(err)
	}
	m, err := e.svc.Member(context.Background(), me.ID)
	if err != nil || m.ID != me.ID || m.DisplayName != testName {
		t.Fatalf("member = %+v, %v", m, err)
	}
	_, err = e.svc.Member(context.Background(), uuid.New())
	wantErr(t, err, model.ErrMemberNotFound)
}

func TestReference(t *testing.T) {
	e := newEnv(t)
	e.store.st.cities = []model.City{{ID: uuid.New(), Code: "city_a", Districts: []model.District{{ID: uuid.New(), Code: "district_a"}}}}
	e.store.st.needs = []string{"autism"}
	// Anonymous: no principal in the context.
	cities, err := e.svc.Cities(context.Background())
	if err != nil || len(cities) != 1 || len(cities[0].Districts) != 1 {
		t.Fatalf("cities = %+v, %v", cities, err)
	}
	needs, err := e.svc.Needs(context.Background())
	if err != nil || len(needs) != 1 {
		t.Fatalf("needs = %v, %v", needs, err)
	}
	boom := errors.New("db down")
	e.store.fail["Cities"], e.store.fail["Needs"] = boom, boom
	if _, err := e.svc.Cities(context.Background()); !errors.Is(err, boom) {
		t.Errorf("cities error = %v", err)
	}
	if _, err := e.svc.Needs(context.Background()); !errors.Is(err, boom) {
		t.Errorf("needs error = %v", err)
	}
}

func TestNoPersonalDataInEvents(t *testing.T) {
	e := newEnv(t)
	ctx, _ := newMember(t, e)
	if _, err := e.svc.UpdateMe(ctx, model.ProfileUpdate{DisplayName: ptr(testName)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RequestVerification(ctx); err != nil {
		t.Fatal(err)
	}
	for _, ev := range e.store.events() {
		b, err := json.Marshal(ev.Data)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), testName) {
			t.Errorf("%s carries the display name: %s", ev.Type, b)
		}
	}
}

func TestStaffListMembersAuthorizationAndAudit(t *testing.T) {
	e := newEnv(t)
	staffID := uuid.New()

	cases := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"anonymous", context.Background(), model.ErrUnauthenticated},
		{"member session", auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: uuid.New()}), model.ErrUnauthenticated},
		{"staff nil identity", auth.WithPrincipal(context.Background(), auth.Principal{Staff: true, MFA: true, Roles: []string{staffauth.RoleModerator}}), model.ErrUnauthenticated},
		{"staff without MFA", auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: staffID, Staff: true, MFA: false, Roles: []string{staffauth.RoleModerator}}), model.ErrForbidden},
		{"staff psychologist", auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: staffID, Staff: true, MFA: true, Roles: []string{staffauth.RolePsychologist}}), model.ErrForbidden},
		{"staff lawyer", auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: staffID, Staff: true, MFA: true, Roles: []string{staffauth.RoleLawyer}}), model.ErrForbidden},
		{"staff moderator with MFA", auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: staffID, Staff: true, MFA: true, Roles: []string{staffauth.RoleModerator}}), nil},
		{"staff admin with MFA", auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: staffID, Staff: true, MFA: true, Roles: []string{staffauth.RoleAdmin}}), nil},
	}

	filter := model.StaffMemberListFilter{Status: model.StaffMemberStatusVerified}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.svc.StaffListMembers(tc.ctx, filter)
			wantErr(t, err, tc.want)
		})
	}

	// Validation and audit checks
	modCtx := auth.WithPrincipal(context.Background(), auth.Principal{
		IdentityID: staffID, Staff: true, MFA: true, Roles: []string{staffauth.RoleModerator},
	})

	// Invalid status
	if _, err := e.svc.StaffListMembers(modCtx, model.StaffMemberListFilter{Status: "pending"}); !errors.Is(err, model.ErrInvalidRequest) {
		t.Errorf("invalid status error = %v, want %v", err, model.ErrInvalidRequest)
	}

	// Invalid cursor
	if _, err := e.svc.StaffListMembers(modCtx, model.StaffMemberListFilter{Status: model.StaffMemberStatusVerified, Cursor: "invalid-cursor"}); !errors.Is(err, model.ErrInvalidRequest) {
		t.Errorf("invalid cursor error = %v, want %v", err, model.ErrInvalidRequest)
	}

	// Audit entry written
	beforeAudits := e.store.st.audits
	page, err := e.svc.StaffListMembers(modCtx, model.StaffMemberListFilter{Status: model.StaffMemberStatusVerified})
	if err != nil {
		t.Fatalf("StaffListMembers: %v", err)
	}
	if page.Items == nil {
		t.Error("expected non-nil Items")
	}
	if e.store.st.audits != beforeAudits+1 {
		t.Errorf("audits = %d, want %d", e.store.st.audits, beforeAudits+1)
	}
}
