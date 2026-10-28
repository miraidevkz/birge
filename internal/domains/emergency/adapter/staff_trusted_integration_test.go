package adapter_test

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

// TestTrustedSosPostgres runs the trusted contact's read and response of a
// childcare SOS, and her decline, against PostgreSQL.
func TestTrustedSosPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.member(false, nil)
	friend := e.member(false, nil)
	invited := e.member(false, nil)
	e.members.Meet(owner.MemberID, friend.MemberID)
	e.members.Meet(owner.MemberID, invited.MemberID)
	c, err := e.svc.AddTrustedContact(ctx, owner, key(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: friend.MemberID})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AcceptTrustedInvitation(ctx, friend, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddTrustedContact(ctx, owner, key(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: invited.MemberID}); err != nil {
		t.Fatal(err)
	}
	sos, err := e.svc.RaiseSos(ctx, owner, key(), model.SosChildcare, fakeText)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.GetTrustedSos(ctx, invited, sos.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("invited read: %v", err)
	}
	v, err := e.svc.GetTrustedSos(ctx, friend, sos.ID)
	if err != nil || v.PlainText != fakeText || v.Type != model.SosChildcare {
		t.Fatalf("friend read: %+v %v", v, err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.audit_log WHERE actor_id = $1 AND actor_role = 'member' AND action = $2 AND object_type = $3 AND object_id = $4`,
		friend.MemberID, model.AuditViewTrustedSos, model.AuditObjectSos, sos.ID); got != 1 {
		t.Fatalf("trusted read audit rows = %d", got)
	}
	for range 2 {
		if err := e.svc.RespondAsTrustedContact(ctx, friend, sos.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.sos_responses WHERE sos_id = $1 AND responder_id = $2`, sos.ID, friend.MemberID); got != 1 {
		t.Fatalf("responses = %d", got)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1 AND payload->>'template' = $2 AND payload->'recipient'->>'member_id' = $3`,
		model.EventDeliveryRequested, model.TemplateSosResponded, owner.MemberID.String()); got != 1 {
		t.Fatalf("author notifications = %d", got)
	}
	if v, err := e.svc.GetTrustedSos(ctx, friend, sos.ID); err != nil || !v.RespondedByMe {
		t.Fatalf("respondedByMe: %+v %v", v, err)
	}

	if err := e.svc.DeclineTrustedInvitation(ctx, friend, c.ID); err != nil {
		t.Fatal(err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.trusted_contacts WHERE id = $1 AND removed_at IS NOT NULL`, c.ID); got != 1 {
		t.Fatal("declined row not kept with removed_at")
	}
	if _, err := e.svc.GetTrustedSos(ctx, friend, sos.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("read after decline: %v", err)
	}
	if err := e.svc.DeclineTrustedInvitation(ctx, friend, c.ID); err != nil {
		t.Fatalf("second decline: %v", err)
	}
}

// TestStaffSosQueuePostgres runs the moderator queue against PostgreSQL:
// order and keyset paging, the first "Seen" kept, the staff close with its
// timer, event and audit rows, and the new schema checks.
func TestStaffSosQueuePostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mod := auth.Principal{IdentityID: uuid.New(), Staff: true, MFA: true, Roles: []string{staffauth.RoleModerator}}
	adm := auth.Principal{IdentityID: uuid.New(), Staff: true, MFA: true, Roles: []string{staffauth.RoleAdmin}}
	d := uuid.New()
	var ids []uuid.UUID
	for range model.StaffPageLimit + 1 {
		m := e.member(true, &d)
		v, err := e.svc.RaiseSos(ctx, m, key(), model.SosOther, fakeText)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
		e.clock.Advance(time.Second)
	}
	page, err := e.svc.ListStaffSos(ctx, mod, model.SosOpen, "")
	if err != nil || len(page.Items) != model.StaffPageLimit || page.NextCursor == "" || page.Items[0].ID != ids[0] {
		t.Fatalf("first page: %d items, %v", len(page.Items), err)
	}
	page2, err := e.svc.ListStaffSos(ctx, mod, model.SosOpen, page.NextCursor)
	if err != nil || len(page2.Items) != 1 || page2.Items[0].ID != ids[len(ids)-1] || page2.NextCursor != "" {
		t.Fatalf("second page: %+v %v", page2.Items, err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.audit_log WHERE actor_id = $1 AND action = $2`, mod.IdentityID, model.AuditViewStaffSos); got != len(ids) {
		t.Fatalf("view audit rows = %d, want %d", got, len(ids))
	}
	// The next poll (within StaffViewAuditDedup on the database clock) adds
	// no view rows for her; another staff member's first listing does.
	if _, err := e.svc.ListStaffSos(ctx, mod, model.SosOpen, ""); err != nil {
		t.Fatal(err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.audit_log WHERE actor_id = $1 AND action = $2`, mod.IdentityID, model.AuditViewStaffSos); got != len(ids) {
		t.Fatalf("view audit rows after a repeat poll = %d, want %d", got, len(ids))
	}
	if _, err := e.svc.ListStaffSos(ctx, adm, model.SosOpen, page.NextCursor); err != nil {
		t.Fatal(err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.audit_log WHERE actor_id = $1 AND action = $2`, adm.IdentityID, model.AuditViewStaffSos); got != 1 {
		t.Fatalf("other staff member's view rows = %d, want 1", got)
	}

	target := ids[0]
	if _, err := e.svc.MarkSosSeen(ctx, mod, target); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkSosSeen(ctx, adm, target); err != nil {
		t.Fatal(err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.sos_requests WHERE id = $1 AND seen_by = $2`, target, mod.IdentityID); got != 1 {
		t.Fatal("first moderator's seen not kept")
	}
	for range 2 {
		if _, err := e.svc.CloseSosAsStaff(ctx, adm, target); err != nil {
			t.Fatal(err)
		}
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.sos_requests WHERE id = $1 AND status = 'closed' AND close_reason = 'author' AND closed_by = $2`, target, adm.IdentityID); got != 1 {
		t.Fatal("staff close not stored")
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1 AND payload->>'sos_id' = $2`, model.EventSosClosed, target.String()); got != 1 {
		t.Fatalf("SosClosed events = %d", got)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.timers WHERE kind = $1 AND subject_id = $2 AND done_at IS NOT NULL AND last_error = 'cancelled'`, model.TimerSosAutoClose, target); got != 1 {
		t.Fatalf("cancelled auto-close timers = %d", got)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.audit_log WHERE object_id = $1 AND action = ANY($2)`, target,
		[]string{model.AuditSosSeen, model.AuditSosCloseStaff}); got != 2 {
		t.Fatalf("seen/close audit rows = %d", got)
	}
	closed, err := e.svc.ListStaffSos(ctx, adm, model.SosClosed, "")
	if err != nil || len(closed.Items) != 1 || closed.Items[0].ID != target {
		t.Fatalf("closed queue: %+v %v", closed.Items, err)
	}

	// Schema: seen_at and seen_by together; closed_by only on a closed request.
	if _, err := e.pool.Exec(ctx, `UPDATE emergency.sos_requests SET seen_at = now() WHERE id = $1`, ids[1]); err == nil {
		t.Fatal("seen_at without seen_by accepted")
	}
	if _, err := e.pool.Exec(ctx, `UPDATE emergency.sos_requests SET closed_by = $2 WHERE id = $1`, ids[1], uuid.New()); err == nil {
		t.Fatal("closed_by on an open request accepted")
	}
}
