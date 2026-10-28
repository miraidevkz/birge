package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/staffauth"
)

// staffQueueRoles may work the SOS queue (requirements section 6:
// moderators; admins oversee them). The first role the caller holds, in
// this order, is the audited role.
var staffQueueRoles = []string{staffauth.RoleModerator, staffauth.RoleAdmin}

// staffActor is an authorized staff member: her staff ID (audit actor,
// seen_by, closed_by) and the role that allowed the call.
type staffActor struct {
	ID   uuid.UUID
	Role string
}

// staffOf authorizes a call to the SOS queue. Deny by default: anonymous
// callers are model.ErrUnauthorized; members, staff without MFA and staff
// without the moderator or admin role are model.ErrForbidden. MFA is checked
// here as well as in staffauth, so the service never relies on the route
// it is mounted under.
func staffOf(p auth.Principal) (staffActor, error) {
	if p.IdentityID == uuid.Nil {
		return staffActor{}, model.ErrUnauthorized
	}
	if !p.Staff || !p.MFA {
		return staffActor{}, model.ErrForbidden
	}
	for _, want := range staffQueueRoles {
		for _, have := range p.Roles {
			if have == want {
				return staffActor{ID: p.IdentityID, Role: want}, nil
			}
		}
	}
	return staffActor{}, model.ErrForbidden
}

func staffAudit(now time.Time, a staffActor, action string, sosID uuid.UUID) model.AuditEntry {
	return model.AuditEntry{At: now, ActorID: a.ID, ActorRole: a.Role, Action: action, ObjectType: model.AuditObjectSos, ObjectID: sosID}
}

// ListStaffSos returns one page of the moderator queue (listStaffSos):
// status open (the default) oldest first, or closed newest first, every
// type including childcare, at most model.StaffPageLimit per page with a
// keyset cursor. Each item carries the text, the author's first name and
// her district, never a phone. One audit entry emergency_sos.view_staff per
// item is written in the listing transaction, before any text is opened,
// unless the same staff member already has one for that SOS from the last
// model.StaffViewAuditDedup (one batched lookup per page): the admin polls
// every 30 seconds, and the repeats would only bury the entries that matter.
// Seen and Close are always audited.
func (s *Service) ListStaffSos(ctx context.Context, p auth.Principal, status, cursor string) (model.StaffSosPage, error) {
	actor, err := staffOf(p)
	if err != nil {
		return model.StaffSosPage{}, err
	}
	if status == "" {
		status = model.SosOpen
	}
	if status != model.SosOpen && status != model.SosClosed {
		return model.StaffSosPage{}, fmt.Errorf("%w: status", model.ErrInvalidInput)
	}
	f := model.StaffSosFilter{Status: status, Limit: model.StaffPageLimit + 1}
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return model.StaffSosPage{}, err
		}
		f.CursorCreatedAt, f.CursorID = &at, &id
	}
	now := s.now()
	var rows []model.Sos
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		if rows, err = tx.ListStaffSos(ctx, f); err != nil {
			return err
		}
		shown := make([]uuid.UUID, 0, len(rows))
		for i, r := range rows {
			if i == model.StaffPageLimit {
				break // the probe row is not shown, so not audited
			}
			shown = append(shown, r.ID)
		}
		recent, err := tx.RecentStaffSosViews(ctx, actor.ID, shown, model.StaffViewAuditDedup)
		if err != nil {
			return err
		}
		for _, id := range shown {
			if recent[id] {
				continue // her view of it within StaffViewAuditDedup is on record
			}
			if err := tx.Audit(ctx, staffAudit(now, actor, model.AuditViewStaffSos, id)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return model.StaffSosPage{}, err
	}
	page := model.StaffSosPage{Items: make([]model.StaffSosView, 0, len(rows))}
	if len(rows) > model.StaffPageLimit {
		rows = rows[:model.StaffPageLimit]
		last := rows[len(rows)-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	authors := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		authors = append(authors, r.MemberID)
	}
	profs, err := s.profiles(ctx, authors...)
	if err != nil {
		return model.StaffSosPage{}, err
	}
	for _, r := range rows {
		page.Items = append(page.Items, s.staffView(ctx, r, profs[r.MemberID]))
	}
	s.log().Info("staff sos listed", "event", "StaffSosListed", "status", status, "items", len(page.Items))
	return page, nil
}

// staffView opens the text for the queue. An SOS whose text cannot be
// opened (the author's key shredded) is still shown, with an empty text:
// a moderator must not lose sight of a request. The profile may be the
// zero value for a member unknown to membership.
func (s *Service) staffView(ctx context.Context, sos model.Sos, prof model.Profile) model.StaffSosView {
	text, err := s.open(ctx, sos.MemberID, sos.Text, model.FieldSosText, sos.ID)
	if err != nil {
		s.log().Warn("sos text unavailable", "event", "SosTextUnavailable", "sos_id", sos.ID)
		text = ""
	}
	return model.StaffSosView{Sos: sos, PlainText: text, DisplayName: prof.DisplayName, MemberDistrictID: prof.DistrictID}
}

// staffViewOne builds the view of one SOS after a staff action.
func (s *Service) staffViewOne(ctx context.Context, sos model.Sos) (model.StaffSosView, error) {
	prof, err := s.profile(ctx, sos.MemberID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return model.StaffSosView{}, err
	}
	return s.staffView(ctx, sos, prof), nil
}

// MarkSosSeen records a moderator's "Seen" (markSosSeen). The first staff
// member and time are kept; a repeat changes nothing. Audited in the same
// transaction: emergency_sos.seen the first time, emergency_sos.view_staff
// on a repeat (the response still shows the SOS). Unknown SOS is
// model.ErrNotFound. Closed SOS may be marked too.
func (s *Service) MarkSosSeen(ctx context.Context, p auth.Principal, sosID uuid.UUID) (model.StaffSosView, error) {
	actor, err := staffOf(p)
	if err != nil {
		return model.StaffSosView{}, err
	}
	now := s.now()
	var out model.Sos
	var marked bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		sos, err := tx.GetSos(ctx, sosID, true)
		if err != nil {
			return err
		}
		out = sos
		updated, ok, err := tx.MarkSosSeen(ctx, sosID, actor.ID, now)
		if err != nil {
			return err
		}
		action := model.AuditViewStaffSos
		if ok {
			updated.ResponseCount = sos.ResponseCount
			out, marked, action = updated, true, model.AuditSosSeen
		}
		return tx.Audit(ctx, staffAudit(now, actor, action, sosID))
	})
	if err != nil {
		return model.StaffSosView{}, err
	}
	if marked {
		s.log().Info("sos seen", "event", "SosSeen", "sos_id", sosID)
	}
	return s.staffViewOne(ctx, out)
}

// CloseSosAsStaff closes an SOS on a moderator's "Close" (closeSosAsStaff)
// with the effects of the author's close: the auto-close timer is
// cancelled, the SOS leaves the district feed (the feed shows open SOS
// only) and emergency.SosClosed is published, in one transaction with the
// audit entry emergency_sos.close_staff. closed_by records the staff ID.
// Closing a closed SOS changes nothing and is audited as
// emergency_sos.view_staff. Unknown SOS is model.ErrNotFound.
func (s *Service) CloseSosAsStaff(ctx context.Context, p auth.Principal, sosID uuid.UUID) (model.StaffSosView, error) {
	actor, err := staffOf(p)
	if err != nil {
		return model.StaffSosView{}, err
	}
	now := s.now()
	var out model.Sos
	var closed bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		sos, err := tx.GetSos(ctx, sosID, true)
		if err != nil {
			return err
		}
		out = sos
		if sos.Status != model.SosOpen {
			return tx.Audit(ctx, staffAudit(now, actor, model.AuditViewStaffSos, sosID))
		}
		by := actor.ID
		updated, ok, err := tx.CloseSos(ctx, sosID, model.CloseByAuthor, &by, now)
		if err != nil {
			return err
		}
		if !ok {
			// Unreachable under the row lock; a repeat is only a read.
			return tx.Audit(ctx, staffAudit(now, actor, model.AuditViewStaffSos, sosID))
		}
		updated.ResponseCount = sos.ResponseCount
		out, closed = updated, true
		if _, err := tx.CancelAutoClose(ctx, sosID, now); err != nil {
			return err
		}
		if err := tx.Publish(ctx, now, model.Event{
			Type: model.EventSosClosed, Version: model.EventVersion, Data: model.SosClosedData{SosID: sosID},
		}); err != nil {
			return err
		}
		return tx.Audit(ctx, staffAudit(now, actor, model.AuditSosCloseStaff, sosID))
	})
	if err != nil {
		return model.StaffSosView{}, err
	}
	if closed {
		s.log().Info("sos closed", "event", model.EventSosClosed, "sos_id", sosID, "reason", "staff")
	}
	return s.staffViewOne(ctx, out)
}
