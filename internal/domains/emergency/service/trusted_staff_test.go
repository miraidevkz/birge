package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/platform/auth"
	"birge/internal/platform/staffauth"
)

var errInjected = errors.New("injected failure")

func moderator() auth.Principal {
	return auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{staffauth.RoleModerator}, MFA: true}
}

func admin() auth.Principal {
	return auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{staffauth.RoleAdmin}, MFA: true}
}

// auditCount counts committed audit entries with action about object id.
func (e *env) auditCount(action string, id uuid.UUID) int {
	n := 0
	for _, a := range e.store.Audit() {
		if a.Action == action && a.ObjectID == id {
			n++
		}
	}
	return n
}

func TestTrustedSosAccess(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.member(false, nil)
	friend := e.member(false, nil)
	invited := e.member(false, nil)
	stranger := e.member(false, nil)
	e.addMember(t, owner, friend, true)
	e.addMember(t, owner, invited, false)
	e.addExternal(t, owner)
	// Childcare: exactly the N5 path, visible to the confirmed contact.
	sos := e.raise(t, owner, model.SosChildcare)

	for name, tc := range map[string]struct {
		p    auth.Principal
		id   uuid.UUID
		want error
	}{
		"anonymous":            {auth.Principal{}, sos.ID, model.ErrUnauthorized},
		"staff":                {moderator(), sos.ID, model.ErrUnauthorized},
		"author herself":       {owner, sos.ID, model.ErrForbidden},
		"non-contact":          {stranger, sos.ID, model.ErrForbidden},
		"invited, unconfirmed": {invited, sos.ID, model.ErrForbidden},
		"unknown sos":          {friend, uuid.New(), model.ErrNotFound},
	} {
		if _, err := e.svc.GetTrustedSos(ctx, tc.p, tc.id); !errors.Is(err, tc.want) {
			t.Errorf("%s: GetTrustedSos = %v, want %v", name, err, tc.want)
		}
		if err := e.svc.RespondAsTrustedContact(ctx, tc.p, tc.id); !errors.Is(err, tc.want) {
			t.Errorf("%s: RespondAsTrustedContact = %v, want %v", name, err, tc.want)
		}
	}
	if n := len(e.store.Audit()); n != 0 {
		t.Fatalf("denied reads wrote %d audit entries", n)
	}

	got, err := e.svc.GetTrustedSos(ctx, friend, sos.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PlainText != fakeText || got.Type != model.SosChildcare || got.OwnerDisplayName != fakeDisplay ||
		got.RespondedByMe || got.Status != model.SosOpen {
		t.Fatalf("trusted view = %+v", got)
	}
	audit := e.store.Audit()
	if len(audit) != 1 || audit[0].Action != model.AuditViewTrustedSos || audit[0].ActorID != friend.MemberID ||
		audit[0].ActorRole != model.AuditActorMember || audit[0].ObjectType != model.AuditObjectSos || audit[0].ObjectID != sos.ID {
		t.Fatalf("audit = %+v", audit)
	}

	// A failed audit returns nothing.
	e.store.FailOn = func(m string) error {
		if m == "Audit" {
			return errInjected
		}
		return nil
	}
	if v, err := e.svc.GetTrustedSos(ctx, friend, sos.ID); !errors.Is(err, errInjected) || v.PlainText != "" {
		t.Fatalf("read with failing audit = %+v, %v", v, err)
	}
	e.store.FailOn = nil

	// Blocked either way: the same 403, block lookups fail closed.
	e.members.Block(owner.MemberID, friend.MemberID)
	if _, err := e.svc.GetTrustedSos(ctx, friend, sos.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("blocked read = %v", err)
	}
	if err := e.svc.RespondAsTrustedContact(ctx, friend, sos.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("blocked respond = %v", err)
	}
}

func TestTrustedSosRemovedContactRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.member(false, nil)
	friend := e.member(false, nil)
	c := e.addMember(t, owner, friend, true)
	sos := e.raise(t, owner, model.SosOther)
	if err := e.svc.RemoveTrustedContact(ctx, owner, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetTrustedSos(ctx, friend, sos.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("removed contact read = %v", err)
	}
}

func TestRespondAsTrustedContact(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.member(false, nil)
	friend := e.member(false, nil)
	e.addMember(t, owner, friend, true)
	sos := e.raise(t, owner, model.SosChildcare)

	for range 2 {
		if err := e.svc.RespondAsTrustedContact(ctx, friend, sos.ID); err != nil {
			t.Fatal(err)
		}
	}
	var told int
	for _, d := range e.store.Deliveries() {
		if d.Template == model.TemplateSosResponded {
			told++
			if d.Recipient.MemberID == nil || *d.Recipient.MemberID != owner.MemberID || d.Class != model.ClassNormal ||
				d.Params["sos_id"] != sos.ID.String() {
				t.Fatalf("delivery = %+v", d)
			}
		}
	}
	if told != 1 {
		t.Fatalf("author notified %d times, want 1", told)
	}
	v, err := e.svc.GetTrustedSos(ctx, friend, sos.ID)
	if err != nil || !v.RespondedByMe {
		t.Fatalf("respondedByMe = %v, %v", v.RespondedByMe, err)
	}

	// The author sees the response like any other and can open a chat.
	list, err := e.svc.ListResponses(ctx, owner, sos.ID)
	if err != nil || len(list) != 1 || list[0].ResponderID != friend.MemberID || !list[0].MetInPerson {
		t.Fatalf("responses = %+v, %v", list, err)
	}
	if err := e.svc.AcceptResponse(ctx, owner, sos.ID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if n := len(e.store.EventsOfType(model.EventSosResponseAccepted)); n != 1 {
		t.Fatalf("accepted events = %d", n)
	}

	// Closed: a new response is 409, a recorded one stays a no-op.
	if _, err := e.svc.CloseSos(ctx, owner, sos.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RespondAsTrustedContact(ctx, friend, sos.ID); err != nil {
		t.Fatalf("retry of a recorded response on a closed sos = %v", err)
	}
	other := e.raise(t, owner, model.SosOther)
	if _, err := e.svc.CloseSos(ctx, owner, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RespondAsTrustedContact(ctx, friend, other.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("respond on closed = %v, want conflict", err)
	}
	// A closed SOS is still readable.
	if v, err := e.svc.GetTrustedSos(ctx, friend, other.ID); err != nil || v.Status != model.SosClosed {
		t.Fatalf("closed read = %+v, %v", v, err)
	}
}

func TestRespondAsTrustedContactRollsBack(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.member(false, nil)
	friend := e.member(false, nil)
	e.addMember(t, owner, friend, true)
	sos := e.raise(t, owner, model.SosOther)
	e.store.FailOn = func(m string) error {
		if m == "Publish:"+model.EventDeliveryRequested {
			return errInjected
		}
		return nil
	}
	if err := e.svc.RespondAsTrustedContact(ctx, friend, sos.ID); !errors.Is(err, errInjected) {
		t.Fatalf("respond = %v", err)
	}
	e.store.FailOn = nil
	list, err := e.svc.ListResponses(ctx, owner, sos.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("responses after rollback = %d, %v", len(list), err)
	}
}

func TestTrustedInvitationReadAndDecline(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.member(false, nil)
	invited := e.member(false, nil)
	other := e.member(false, nil)
	c := e.addMember(t, owner, invited, false)

	inv, err := e.svc.GetTrustedInvitation(ctx, invited, c.ID)
	if err != nil || inv.ContactID != c.ID || inv.Status != model.ContactInvited || inv.OwnerDisplayName != fakeDisplay ||
		!inv.InvitedAt.Equal(e.clock.Now()) {
		t.Fatalf("invitation = %+v, %v", inv, err)
	}
	for name, p := range map[string]auth.Principal{"owner": owner, "other member": other} {
		if _, err := e.svc.GetTrustedInvitation(ctx, p, c.ID); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("%s read = %v", name, err)
		}
		if err := e.svc.DeclineTrustedInvitation(ctx, p, c.ID); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("%s decline = %v", name, err)
		}
	}
	for name, p := range map[string]auth.Principal{"anonymous": {}, "staff": moderator()} {
		if _, err := e.svc.GetTrustedInvitation(ctx, p, c.ID); !errors.Is(err, model.ErrUnauthorized) {
			t.Errorf("%s read = %v", name, err)
		}
		if err := e.svc.DeclineTrustedInvitation(ctx, p, c.ID); !errors.Is(err, model.ErrUnauthorized) {
			t.Errorf("%s decline = %v", name, err)
		}
	}
	if _, err := e.svc.GetTrustedInvitation(ctx, invited, uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown invitation = %v", err)
	}
	// An external entry is never an invitation.
	ext := e.addExternal(t, owner)
	if _, err := e.svc.GetTrustedInvitation(ctx, invited, ext.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("external entry = %v", err)
	}

	events := len(e.store.Events())
	for range 2 {
		if err := e.svc.DeclineTrustedInvitation(ctx, invited, c.ID); err != nil {
			t.Fatalf("decline = %v", err)
		}
	}
	if got, _ := e.store.Contact(c.ID); got.RemovedAt == nil {
		t.Fatal("declined entry not removed")
	}
	if n := len(e.store.Events()); n != events {
		t.Fatalf("decline published %d events; the owner must not be told", n-events)
	}
	if _, err := e.svc.GetTrustedInvitation(ctx, invited, c.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("read after decline = %v", err)
	}
	if err := e.svc.AcceptTrustedInvitation(ctx, invited, c.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("accept after decline = %v", err)
	}
	plan, err := e.svc.GetPlan(ctx, owner)
	if err != nil || len(plan.Contacts) != 1 || plan.Contacts[0].ID != ext.ID {
		t.Fatalf("owner's contacts after decline = %+v, %v", plan.Contacts, err)
	}
}

func TestDeclineByConfirmedContactEndsAccessAndRevokesLinks(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.member(false, nil)
	friend := e.member(false, nil)
	c := e.addMember(t, owner, friend, true)
	if err := e.svc.SetChildCard(ctx, owner, fakeCard); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SendPlan(ctx, owner, uuid.Must(uuid.NewV7()), "SEND"); err != nil {
		t.Fatal(err)
	}
	sos := e.raise(t, owner, model.SosChildcare)
	if _, err := e.svc.GetSharedPlan(ctx, friend, owner.MemberID); err != nil {
		t.Fatal(err)
	}
	if inv, err := e.svc.GetTrustedInvitation(ctx, friend, c.ID); err != nil || inv.Status != model.ContactConfirmed {
		t.Fatalf("confirmed entry = %+v, %v", inv, err)
	}

	// The link revocation is part of the same transaction: if it fails,
	// nothing is removed.
	e.store.FailOn = func(m string) error {
		if m == "RevokeContactPlanLinks" {
			return errInjected
		}
		return nil
	}
	if err := e.svc.DeclineTrustedInvitation(ctx, friend, c.ID); !errors.Is(err, errInjected) {
		t.Fatalf("decline with failing revoke = %v", err)
	}
	e.store.FailOn = nil
	if got, _ := e.store.Contact(c.ID); got.RemovedAt != nil {
		t.Fatal("entry removed although the revocation failed")
	}

	if err := e.svc.DeclineTrustedInvitation(ctx, friend, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetSharedPlan(ctx, friend, owner.MemberID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("shared plan after decline = %v", err)
	}
	if _, err := e.svc.GetTrustedSos(ctx, friend, sos.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("trusted sos after decline = %v", err)
	}
	if _, _, err := e.svc.TrustedContactTarget(ctx, c.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("notify lookup after decline = %v", err)
	}
}

func TestStaffSosDenyByDefault(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.member(false, nil)
	sos := e.raise(t, owner, model.SosOther)
	noMFA := moderator()
	noMFA.MFA = false
	for name, tc := range map[string]struct {
		p    auth.Principal
		want error
	}{
		"anonymous":             {auth.Principal{}, model.ErrUnauthorized},
		"member":                {owner, model.ErrForbidden},
		"moderator without MFA": {noMFA, model.ErrForbidden},
		"psychologist":          {auth.Principal{IdentityID: uuid.New(), Staff: true, MFA: true, Roles: []string{staffauth.RolePsychologist}}, model.ErrForbidden},
		"lawyer":                {auth.Principal{IdentityID: uuid.New(), Staff: true, MFA: true, Roles: []string{staffauth.RoleLawyer}}, model.ErrForbidden},
		"staff without roles":   {auth.Principal{IdentityID: uuid.New(), Staff: true, MFA: true}, model.ErrForbidden},
		"roles without staff":   {auth.Principal{IdentityID: uuid.New(), MFA: true, Roles: []string{staffauth.RoleModerator}}, model.ErrForbidden},
	} {
		if _, err := e.svc.ListStaffSos(ctx, tc.p, "", ""); !errors.Is(err, tc.want) {
			t.Errorf("%s: list = %v, want %v", name, err, tc.want)
		}
		if _, err := e.svc.MarkSosSeen(ctx, tc.p, sos.ID); !errors.Is(err, tc.want) {
			t.Errorf("%s: seen = %v, want %v", name, err, tc.want)
		}
		if _, err := e.svc.CloseSosAsStaff(ctx, tc.p, sos.ID); !errors.Is(err, tc.want) {
			t.Errorf("%s: close = %v, want %v", name, err, tc.want)
		}
	}
	got, _ := e.store.Sos(sos.ID)
	if got.Status != model.SosOpen || got.SeenAt != nil || len(e.store.Audit()) != 0 {
		t.Fatalf("denied calls changed state: %+v, audit %d", got, len(e.store.Audit()))
	}
	if _, err := e.svc.ListStaffSos(ctx, moderator(), "pending", ""); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("unknown status = %v", err)
	}
	if _, err := e.svc.ListStaffSos(ctx, moderator(), "", "not-a-cursor"); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad cursor = %v", err)
	}
	if _, err := e.svc.MarkSosSeen(ctx, moderator(), uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("seen unknown = %v", err)
	}
	if _, err := e.svc.CloseSosAsStaff(ctx, admin(), uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("close unknown = %v", err)
	}
}

func TestStaffSosQueueOrderPagingAndAudit(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	mod := moderator()
	d := district()
	var ids []uuid.UUID
	// 52 open requests from different members (3 per hour each), one minute apart.
	for i := range model.StaffPageLimit + 2 {
		m := e.member(true, d)
		typ := model.SosOther
		if i == 0 {
			typ = model.SosChildcare // moderators see childcare (N5: moderators and trusted contacts)
		}
		ids = append(ids, e.raise(t, m, typ).ID)
		e.clock.Advance(time.Minute)
	}
	closer := e.member(false, nil)
	c1 := e.raise(t, closer, model.SosOther)
	e.clock.Advance(time.Minute)
	c2 := e.raise(t, closer, model.SosOther)
	for _, id := range []uuid.UUID{c1.ID, c2.ID} {
		if _, err := e.svc.CloseSos(ctx, closer, id); err != nil {
			t.Fatal(err)
		}
	}

	page, err := e.svc.ListStaffSos(ctx, mod, model.SosOpen, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != model.StaffPageLimit || page.NextCursor == "" {
		t.Fatalf("first page: %d items, cursor %q", len(page.Items), page.NextCursor)
	}
	first := page.Items[0]
	if first.ID != ids[0] || first.Type != model.SosChildcare || first.PlainText != fakeText ||
		first.DisplayName != fakeDisplay || first.MemberDistrictID == nil || *first.MemberDistrictID != *d {
		t.Fatalf("oldest item = %+v", first)
	}
	page2, err := e.svc.ListStaffSos(ctx, mod, "", page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 2 || page2.NextCursor != "" {
		t.Fatalf("second page: %d items, cursor %q", len(page2.Items), page2.NextCursor)
	}
	got := make([]uuid.UUID, 0, len(ids))
	for _, it := range append(page.Items, page2.Items...) {
		got = append(got, it.ID)
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("open queue order differs at %d", i)
		}
	}
	// One audit entry per item shown, none for the probe row.
	views := 0
	for _, a := range e.store.Audit() {
		if a.Action == model.AuditViewStaffSos {
			views++
			if a.ActorID != mod.IdentityID || a.ActorRole != staffauth.RoleModerator || a.ObjectType != model.AuditObjectSos {
				t.Fatalf("audit = %+v", a)
			}
		}
	}
	if views != len(ids) {
		t.Fatalf("view audits = %d, want %d", views, len(ids))
	}

	closed, err := e.svc.ListStaffSos(ctx, admin(), model.SosClosed, "")
	if err != nil || len(closed.Items) != 2 || closed.Items[0].ID != c2.ID || closed.Items[1].ID != c1.ID {
		t.Fatalf("closed queue (newest first) = %+v, %v", closed.Items, err)
	}
}

func TestStaffSeenAndClose(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := district()
	author := e.member(true, d)
	neighbour := e.member(true, d)
	sos := e.raise(t, author, model.SosOther)
	if _, err := e.svc.ShareSos(ctx, author, sos.ID); err != nil {
		t.Fatal(err)
	}
	mod, adm := moderator(), admin()

	seen, err := e.svc.MarkSosSeen(ctx, mod, sos.ID)
	if err != nil || seen.SeenBy == nil || *seen.SeenBy != mod.IdentityID || seen.SeenAt == nil || seen.PlainText != fakeText {
		t.Fatalf("seen = %+v, %v", seen, err)
	}
	firstAt := *seen.SeenAt
	e.clock.Advance(time.Minute)
	again, err := e.svc.MarkSosSeen(ctx, adm, sos.ID)
	if err != nil || *again.SeenBy != mod.IdentityID || !again.SeenAt.Equal(firstAt) {
		t.Fatalf("second seen = %+v, %v", again, err)
	}
	if e.auditCount(model.AuditSosSeen, sos.ID) != 1 || e.auditCount(model.AuditViewStaffSos, sos.ID) != 1 {
		t.Fatalf("seen audit = %+v", e.store.Audit())
	}

	// Close: rolls back as a whole when the audit fails.
	e.store.FailOn = func(m string) error {
		if m == "Audit" {
			return errInjected
		}
		return nil
	}
	if _, err := e.svc.CloseSosAsStaff(ctx, mod, sos.ID); !errors.Is(err, errInjected) {
		t.Fatalf("close with failing audit = %v", err)
	}
	e.store.FailOn = nil
	if got, _ := e.store.Sos(sos.ID); got.Status != model.SosOpen || len(e.store.EventsOfType(model.EventSosClosed)) != 0 {
		t.Fatal("close committed without its audit entry")
	}

	for range 2 {
		v, err := e.svc.CloseSosAsStaff(ctx, mod, sos.ID)
		if err != nil || v.Status != model.SosClosed || v.ClosedBy == nil || *v.ClosedBy != mod.IdentityID ||
			v.CloseReason == nil || *v.CloseReason != model.CloseByAuthor || v.ClosedAt == nil {
			t.Fatalf("close = %+v, %v", v, err)
		}
	}
	if n := len(e.store.EventsOfType(model.EventSosClosed)); n != 1 {
		t.Fatalf("SosClosed events = %d, want 1", n)
	}
	if tm, ok := e.store.Timer(sos.ID); !ok || !tm.Cancelled {
		t.Fatalf("auto-close timer = %+v, %v", tm, ok)
	}
	if e.auditCount(model.AuditSosCloseStaff, sos.ID) != 1 || e.auditCount(model.AuditViewStaffSos, sos.ID) != 2 {
		t.Fatalf("close audit = %+v", e.store.Audit())
	}
	page, err := e.svc.Feed(ctx, neighbour, "")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("feed after staff close = %+v, %v", page.Items, err)
	}
	// The author's own close of it is now a no-op.
	if _, err := e.svc.CloseSos(ctx, author, sos.ID); err != nil || len(e.store.EventsOfType(model.EventSosClosed)) != 1 {
		t.Fatalf("author close after staff close = %v", err)
	}
	// The timer firing later does nothing either.
	tx, commit := e.store.Tx()
	if err := e.svc.HandleAutoClose(ctx, tx, sos.ID); err != nil {
		t.Fatal(err)
	}
	commit()
	if n := len(e.store.EventsOfType(model.EventSosClosed)); n != 1 {
		t.Fatalf("SosClosed events after timer = %d", n)
	}
}
