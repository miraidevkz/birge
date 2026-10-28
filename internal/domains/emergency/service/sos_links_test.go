package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/service"
	"birge/internal/platform/auth"
)

// sosWithExternal sets up an owner with an external and a confirmed member
// contact and raises an SOS of type typ.
func (e *linkEnv) sosWithExternal(t *testing.T, typ string) (owner auth.Principal, ext, member model.ContactView, sos model.SosView) {
	t.Helper()
	owner = e.member(false, nil)
	ext = e.addExternal(t, owner)
	member = e.addMember(t, owner, e.member(true, nil), true)
	sos = e.raise(t, owner, typ)
	return owner, ext, member, sos
}

func (e *linkEnv) mintSos(t *testing.T, contactID, sosID uuid.UUID) string {
	t.Helper()
	tok, err := e.svc.ExternalSosLink(context.Background(), contactID, sosID)
	if err != nil {
		t.Fatalf("mint sos link: %v", err)
	}
	if len(tok) != 43 || strings.ContainsAny(tok, "+/=") {
		t.Fatalf("token shape: %d characters", len(tok))
	}
	e.tokens = append(e.tokens, tok)
	return tok
}

// viewSosCalls views tok and returns the error and the store calls it made.
func (e *linkEnv) viewSosCalls(tok string) (model.ExternalSos, []string, error) {
	e.mu.Lock()
	e.calls = nil
	e.mu.Unlock()
	v, err := e.svc.ViewExternalSos(context.Background(), tok)
	e.mu.Lock()
	defer e.mu.Unlock()
	return v, append([]string(nil), e.calls...), err
}

func TestExternalSosLinkLifecycle(t *testing.T) {
	e := newLinkEnv(t)
	ctx := context.Background()
	raisedAt := e.clock.Now()
	// Childcare on purpose: an external contact is one of her trusted
	// contacts, the people N5 allows (and the SMS goes to her anyway).
	owner, ext, member, sos := e.sosWithExternal(t, model.SosChildcare)

	other := e.member(false, nil)
	otherSos := e.raise(t, other, model.SosOther)
	for name, tc := range map[string][2]uuid.UUID{
		"member contact":       {member.ID, sos.ID},
		"unknown contact":      {uuid.New(), sos.ID},
		"unknown sos":          {ext.ID, uuid.New()},
		"another member's sos": {ext.ID, otherSos.ID},
		"nil ids":              {uuid.Nil, uuid.Nil},
	} {
		if _, err := e.svc.ExternalSosLink(ctx, tc[0], tc[1]); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}

	e.clock.Advance(time.Minute) // the SMS goes out a little later
	t1 := e.mintSos(t, ext.ID, sos.ID)
	links := e.store.SosLinks()
	want := raisedAt.Add(24 * time.Hour)
	if len(links) != 1 || !links[0].ExpiresAt.Equal(want) || links[0].OwnerID != owner.MemberID ||
		links[0].SosID != sos.ID || links[0].ContactID != ext.ID || len(links[0].TokenHash) != 32 {
		t.Fatalf("link = %+v", links)
	}
	if tm, ok := e.store.SosPurgeTimer(links[0].ID); !ok || !tm.DueAt.Equal(want.Add(7*24*time.Hour)) {
		t.Fatalf("purge timer = %+v %v", tm, ok)
	}
	v, calls, err := e.viewSosCalls(t1)
	if err != nil || v.Text != fakeText || v.OwnerDisplayName != fakeDisplay || v.Type != model.SosChildcare ||
		v.Status != model.SosOpen || !v.CreatedAt.Equal(raisedAt) || !v.ClosesAt.Equal(want) {
		t.Fatalf("view = %+v %v", v, err)
	}
	if len(calls) != 2 || calls[0] != "GetLiveSosLink" || calls[1] != "Audit" {
		t.Fatalf("view store calls = %v, want lookup then audit", calls)
	}
	audit := e.store.Audit()
	last := audit[len(audit)-1]
	if last.Action != model.AuditViewExternalSos || last.ActorID != ext.ID || last.ActorRole != model.AuditActorExternalContact ||
		last.ObjectType != model.AuditObjectSos || last.ObjectID != sos.ID || last.Reason != "" {
		t.Fatalf("audit = %+v", last)
	}

	// A new link for the same SOS and contact (a retried SMS) revokes the old one.
	t2 := e.mintSos(t, ext.ID, sos.ID)
	if _, _, err := e.viewSosCalls(t1); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("superseded token: %v", err)
	}
	if _, _, err := e.viewSosCalls(t2); err != nil {
		t.Fatalf("new token: %v", err)
	}

	// Closing the SOS ends the link and minting.
	if _, err := e.svc.CloseSos(ctx, owner, sos.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.viewSosCalls(t2); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("token of a closed sos: %v", err)
	}
	if _, err := e.svc.ExternalSosLink(ctx, ext.ID, sos.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("mint for a closed sos: %v", err)
	}
}

// TestExternalSosLinkEndsWithAutoClose: with a shorter auto-close the link
// expires at closes_at; with a longer one it still dies 24 hours after the
// SOS was raised, even while the SOS is open.
func TestExternalSosLinkEndsWithAutoClose(t *testing.T) {
	for name, tc := range map[string]struct {
		autoClose, wantTTL time.Duration
	}{
		"auto-close first": {autoClose: 2 * time.Hour, wantTTL: 2 * time.Hour},
		"24 hours first":   {autoClose: 48 * time.Hour, wantTTL: 24 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLinkEnv(t)
			e.svc.Settings.AutoCloseAfter = tc.autoClose
			raisedAt := e.clock.Now()
			_, ext, _, sos := e.sosWithExternal(t, model.SosOther)
			tok := e.mintSos(t, ext.ID, sos.ID)
			if l := e.store.SosLinks()[0]; !l.ExpiresAt.Equal(raisedAt.Add(tc.wantTTL)) {
				t.Fatalf("expires = %v", l.ExpiresAt)
			}
			e.clock.T = raisedAt.Add(tc.wantTTL - time.Second)
			if _, _, err := e.viewSosCalls(tok); err != nil {
				t.Fatalf("before expiry: %v", err)
			}
			e.clock.T = raisedAt.Add(tc.wantTTL)
			if _, _, err := e.viewSosCalls(tok); !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("at expiry: %v", err)
			}
			if _, err := e.svc.ExternalSosLink(context.Background(), ext.ID, sos.ID); !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("mint after expiry: %v", err)
			}
		})
	}
}

func TestExternalSosLinkEndsOnContactRemoval(t *testing.T) {
	e := newLinkEnv(t)
	ctx := context.Background()
	owner, ext, _, sos := e.sosWithExternal(t, model.SosOther)
	tok := e.mintSos(t, ext.ID, sos.ID)
	if err := e.svc.RemoveTrustedContact(ctx, owner, ext.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.viewSosCalls(tok); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("token of a removed contact: %v", err)
	}
	if _, err := e.svc.ExternalSosLink(ctx, ext.ID, sos.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("mint for a removed contact: %v", err)
	}
}

// TestPlanAndSosTokensNeverCross: a plan token does not open an SOS and an
// SOS token does not open a plan, although both use the same key.
func TestPlanAndSosTokensNeverCross(t *testing.T) {
	e := newLinkEnv(t)
	ctx := context.Background()
	owner, ext, _, sos := e.sosWithExternal(t, model.SosOther)
	if err := e.svc.SetChildCard(ctx, owner, fakeCard); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SendPlan(ctx, owner, uuid.New(), "SEND"); err != nil {
		t.Fatal(err)
	}
	planTok := e.mint(t, ext.ID)
	sosTok := e.mintSos(t, ext.ID, sos.ID)
	if _, err := e.svc.ViewExternalSos(ctx, planTok); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("plan token on the sos view: %v", err)
	}
	if _, err := e.svc.ViewExternalPlan(ctx, sosTok); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("sos token on the plan view: %v", err)
	}
	if string(e.store.SosLinks()[0].TokenHash) == string(e.store.PlanLinks()[0].TokenHash) {
		t.Fatal("hashes collide")
	}
	// Each still opens its own page.
	if _, err := e.svc.ViewExternalSos(ctx, sosTok); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ViewExternalPlan(ctx, planTok); err != nil {
		t.Fatal(err)
	}
}

// TestExternalSosFailuresLookAlike: unknown, revoked, expired and closed
// all fail with ErrNotFound after exactly one store lookup and no audit.
func TestExternalSosFailuresLookAlike(t *testing.T) {
	e := newLinkEnv(t)
	ctx := context.Background()
	owner, ext, _, sos := e.sosWithExternal(t, model.SosOther)
	revoked := e.mintSos(t, ext.ID, sos.ID)
	live := e.mintSos(t, ext.ID, sos.ID) // revokes the first
	e.clock.Advance(time.Minute)
	sos2 := e.raise(t, owner, model.SosItemOrMedicine)
	closed := e.mintSos(t, ext.ID, sos2.ID)
	if _, err := e.svc.CloseSos(ctx, owner, sos2.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(25 * time.Hour) // live is expired now
	auditBefore := len(e.store.Audit())

	for name, tok := range map[string]string{
		"unknown": strings.Repeat("A", 43),
		"revoked": revoked,
		"expired": live,
		"closed":  closed,
	} {
		_, calls, err := e.viewSosCalls(tok)
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
		if len(calls) != 1 || calls[0] != "GetLiveSosLink" {
			t.Errorf("%s: store calls %v, want exactly [GetLiveSosLink]", name, calls)
		}
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", 44), strings.Repeat("+", 43)} {
		v, calls, err := e.viewSosCalls(bad)
		if !errors.Is(err, model.ErrNotFound) || len(calls) != 0 || v.Text != "" {
			t.Errorf("malformed %q: %v, calls %v", bad, err, calls)
		}
	}
	if n := len(e.store.Audit()); n != auditBefore {
		t.Fatalf("failed views wrote %d audit entries", n-auditBefore)
	}
}

func TestExternalSosAuditFailureShowsNothing(t *testing.T) {
	e := newLinkEnv(t)
	_, ext, _, sos := e.sosWithExternal(t, model.SosOther)
	tok := e.mintSos(t, ext.ID, sos.ID)
	boom := errors.New("audit down")
	e.store.FailOn = func(method string) error {
		if method == "Audit" {
			return boom
		}
		return nil
	}
	if v, err := e.svc.ViewExternalSos(context.Background(), tok); !errors.Is(err, boom) || v.Text != "" {
		t.Fatalf("view with failing audit = %+v %v", v, err)
	}
}

func TestExternalSosLinkMintRollsBack(t *testing.T) {
	e := newLinkEnv(t)
	_, ext, _, sos := e.sosWithExternal(t, model.SosOther)
	old := e.mintSos(t, ext.ID, sos.ID)
	e.store.FailOn = func(method string) error {
		if method == "ScheduleSosLinkPurge" {
			return errors.New("timers down")
		}
		return nil
	}
	if _, err := e.svc.ExternalSosLink(context.Background(), ext.ID, sos.ID); err == nil {
		t.Fatal("mint with failing timer succeeded")
	}
	e.store.FailOn = nil
	if len(e.store.SosLinks()) != 1 {
		t.Fatalf("links after rollback = %d", len(e.store.SosLinks()))
	}
	if _, err := e.svc.ViewExternalSos(context.Background(), old); err != nil {
		t.Fatalf("old link revoked by a rolled-back mint: %v", err)
	}
}

func TestSosLinkPurge(t *testing.T) {
	e := newLinkEnv(t)
	ctx := context.Background()
	_, ext, _, sos := e.sosWithExternal(t, model.SosOther)
	e.mintSos(t, ext.ID, sos.ID)
	id := e.store.SosLinks()[0].ID

	run := func() {
		tx, commit := e.store.Tx()
		if err := e.svc.HandleSosLinkPurge(ctx, tx, id); err != nil {
			t.Fatal(err)
		}
		commit()
	}
	e.clock.Advance(24*time.Hour + 7*24*time.Hour - time.Minute)
	run()
	if len(e.store.SosLinks()) != 1 {
		t.Fatal("purged before the retention period")
	}
	e.clock.Advance(time.Minute)
	run()
	if len(e.store.SosLinks()) != 0 {
		t.Fatal("not purged after the retention period")
	}
	run() // already gone: nothing to do
}

func TestSosLinkNeedsKey(t *testing.T) {
	e := newLinkEnv(t)
	_, ext, _, sos := e.sosWithExternal(t, model.SosOther)
	e.svc.Settings = service.Settings{}
	if _, err := e.svc.ExternalSosLink(context.Background(), ext.ID, sos.ID); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("mint without key: %v", err)
	}
	if _, err := e.svc.ViewExternalSos(context.Background(), strings.Repeat("A", 43)); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("view without key: %v", err)
	}
}

// TestStaffSosViewAuditDeduplicated: the admin polls the queue every 30
// seconds; one view entry per staff member and SOS covers ten minutes.
// Another staff member, an SOS new on the page and the next window are
// audited again; Seen and Close always are.
func TestStaffSosViewAuditDeduplicated(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	mod, mod2 := moderator(), moderator()
	a := e.raise(t, e.member(false, nil), model.SosOther)
	views := func(staff uuid.UUID, id uuid.UUID) int {
		n := 0
		for _, x := range e.store.Audit() {
			if x.Action == model.AuditViewStaffSos && x.ActorID == staff && x.ObjectID == id {
				n++
			}
		}
		return n
	}
	list := func(p auth.Principal) {
		t.Helper()
		if _, err := e.svc.ListStaffSos(ctx, p, model.SosOpen, ""); err != nil {
			t.Fatal(err)
		}
	}

	list(mod)
	for range 5 { // polls for 2.5 minutes
		e.clock.Advance(30 * time.Second)
		list(mod)
	}
	if n := views(mod.IdentityID, a.ID); n != 1 {
		t.Fatalf("view entries after polling = %d, want 1", n)
	}

	b := e.raise(t, e.member(false, nil), model.SosOther)
	list(mod)
	if views(mod.IdentityID, a.ID) != 1 || views(mod.IdentityID, b.ID) != 1 {
		t.Fatal("a new item on the page must be audited once, the old one not again")
	}
	list(mod2)
	if views(mod2.IdentityID, a.ID) != 1 {
		t.Fatal("another staff member's first view must be audited")
	}

	if _, err := e.svc.MarkSosSeen(ctx, mod, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkSosSeen(ctx, mod, a.ID); err != nil { // a repeat is a view, always audited
		t.Fatal(err)
	}
	if views(mod.IdentityID, a.ID) != 2 || e.auditCount(model.AuditSosSeen, a.ID) != 1 {
		t.Fatal("seen actions must always be audited")
	}

	e.clock.Advance(model.StaffViewAuditDedup)
	list(mod)
	if n := views(mod.IdentityID, b.ID); n != 2 {
		t.Fatalf("view entries after the window = %d, want 2", n)
	}

	boom := errors.New("audit log down")
	e.store.FailOn = func(m string) error {
		if m == "RecentStaffSosViews" {
			return boom
		}
		return nil
	}
	if _, err := e.svc.ListStaffSos(ctx, mod, model.SosOpen, ""); !errors.Is(err, boom) {
		t.Fatalf("listing without the dedup lookup = %v, want the error (fail closed: nothing shown unaudited)", err)
	}
}
