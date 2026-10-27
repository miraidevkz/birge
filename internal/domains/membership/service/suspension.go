package service

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/staffauth"
)

// GetSuspension returns the suspension state of a member. Staff with role
// moderator or admin and MFA (deny by default).
func (s *Service) GetSuspension(ctx context.Context, memberID uuid.UUID) (model.Suspension, error) {
	if _, err := staffModerator(ctx); err != nil {
		return model.Suspension{}, err
	}
	if memberID == uuid.Nil {
		return model.Suspension{}, model.ErrMemberNotFound
	}
	var out model.Suspension
	err := s.store.InTx(ctx, func(tx port.Tx) error {
		rec, err := tx.MemberByID(ctx, memberID)
		if err != nil {
			return err
		}
		out = model.Suspension{
			MemberID: memberID, Suspended: rec.SuspendedAt != nil,
			SuspendedAt: rec.SuspendedAt, Reason: rec.SuspendedReason,
		}
		return nil
	})
	return out, err
}

// LiftSuspension clears a member's suspension. Staff admin with MFA only.
// Idempotent when not suspended. Audited.
func (s *Service) LiftSuspension(ctx context.Context, memberID uuid.UUID) error {
	p, err := staffAdmin(ctx)
	if err != nil {
		return err
	}
	if memberID == uuid.Nil {
		return model.ErrMemberNotFound
	}
	now := s.clock.Now().UTC()
	return s.store.InTx(ctx, func(tx port.Tx) error {
		rec, err := tx.MemberByID(ctx, memberID)
		if err != nil {
			return err
		}
		if rec.SuspendedAt == nil {
			return nil
		}
		if err := tx.SetSuspension(ctx, memberID, nil, "", now); err != nil {
			return err
		}
		return tx.Audit(ctx, p.IdentityID, model.ActorRoleStaff, model.AuditLiftSuspension,
			model.AuditObjectMember, memberID, "")
	})
}

// CrisisMemberID resolves the signed-in member like Me but ignores a
// suspension. Only the crisis and own-data routes use it (wiring
// crisisExempt): a suspended member keeps the help button, SOS to her own
// trusted contacts, the emergency plan, consents and deletion
// (non-negotiable 1). Everything else answers 403 member_suspended.
func (s *Service) CrisisMemberID(ctx context.Context) (uuid.UUID, error) {
	identity, err := identityOf(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = s.store.InTx(ctx, func(tx port.Tx) error {
		me, err := s.ensure(ctx, tx, identity, s.clock.Now())
		id = me.ID
		return err
	})
	return id, err
}

// staffModerator authorizes a staff call with moderator or admin and MFA.
func staffModerator(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.FromContext(ctx)
	if !ok || !p.Staff || p.IdentityID == uuid.Nil {
		return auth.Principal{}, model.ErrUnauthenticated
	}
	if !p.MFA {
		return auth.Principal{}, model.ErrForbidden
	}
	if !slices.Contains(p.Roles, staffauth.RoleModerator) && !slices.Contains(p.Roles, staffauth.RoleAdmin) {
		return auth.Principal{}, model.ErrForbidden
	}
	return p, nil
}

// staffAdmin authorizes a staff call with admin and MFA.
func staffAdmin(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.FromContext(ctx)
	if !ok || !p.Staff || p.IdentityID == uuid.Nil {
		return auth.Principal{}, model.ErrUnauthenticated
	}
	if !p.MFA || !slices.Contains(p.Roles, staffauth.RoleAdmin) {
		return auth.Principal{}, model.ErrForbidden
	}
	return p, nil
}
