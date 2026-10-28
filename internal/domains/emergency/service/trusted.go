package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
	"birge/internal/platform/auth"
)

// trustedSos returns the SOS for viewer acting as a trusted contact of its
// author, inside tx: model.ErrNotFound for an unknown SOS, model.ErrForbidden
// unless viewer is an active, confirmed member contact of the author (an
// invited, removed or external entry is not). Every SOS type passes,
// childcare included: trusted contacts are exactly who may help with her
// child (AGENTS.md non-negotiable 5). The check runs in the caller's
// transaction, so an audit entry written next commits only with it.
func trustedSos(ctx context.Context, tx port.Tx, viewer, sosID uuid.UUID, forUpdate bool) (model.Sos, error) {
	sos, err := tx.GetSos(ctx, sosID, forUpdate)
	if err != nil {
		return model.Sos{}, err
	}
	if sos.MemberID == viewer {
		return model.Sos{}, model.ErrForbidden
	}
	if _, err := tx.GetSharedPlanContact(ctx, sos.MemberID, viewer); errors.Is(err, model.ErrNotFound) {
		return model.Sos{}, model.ErrForbidden
	} else if err != nil {
		return model.Sos{}, err
	}
	return sos, nil
}

// sosAuthorFor returns the author of the SOS for the block check that comes
// before the trusted-contact transaction (membership is not called inside
// a database transaction). An unknown SOS is model.ErrNotFound.
func (s *Service) sosAuthorFor(ctx context.Context, viewer, sosID uuid.UUID) (uuid.UUID, error) {
	var author uuid.UUID
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		sos, err := tx.GetSos(ctx, sosID, false)
		if err != nil {
			return err
		}
		author = sos.MemberID
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	if author == viewer {
		return uuid.Nil, model.ErrForbidden
	}
	// Blocks hide both ways (ADR-0019) and fail closed. A blocked pair is
	// the same 403 as a non-contact, so the block is not revealed.
	b, err := s.blocked(ctx, viewer, author)
	if err != nil {
		return uuid.Nil, err
	}
	if b {
		return uuid.Nil, model.ErrForbidden
	}
	return author, nil
}

// GetTrustedSos returns an SOS to a confirmed trusted contact of its author
// (getTrustedSos): any type, childcare included, open or closed. An unknown
// SOS is model.ErrNotFound; anyone who is not an active, confirmed member
// contact of the author (also an invited one, the author herself, or a
// member blocked either way) is model.ErrForbidden without saying why.
// The audit entry emergency_sos.view_trusted is written in the same
// transaction as the access check, before the text is opened; a failed audit
// returns nothing. The API answers with Cache-Control: no-store.
func (s *Service) GetTrustedSos(ctx context.Context, p auth.Principal, sosID uuid.UUID) (model.TrustedSosView, error) {
	viewer, err := memberOf(p)
	if err != nil {
		return model.TrustedSosView{}, err
	}
	author, err := s.sosAuthorFor(ctx, viewer, sosID)
	if err != nil {
		return model.TrustedSosView{}, err
	}
	now := s.now()
	var sos model.Sos
	var responded bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		if sos, err = trustedSos(ctx, tx, viewer, sosID, false); err != nil {
			return err
		}
		if responded, err = tx.SosRespondedBy(ctx, sosID, viewer); err != nil {
			return err
		}
		return tx.Audit(ctx, model.AuditEntry{
			At: now, ActorID: viewer, ActorRole: model.AuditActorMember, Action: model.AuditViewTrustedSos,
			ObjectType: model.AuditObjectSos, ObjectID: sosID,
		})
	})
	if err != nil {
		return model.TrustedSosView{}, err
	}
	s.log().Info("trusted sos viewed", "event", "TrustedSosViewed", "sos_id", sosID)
	text, err := s.open(ctx, author, sos.Text, model.FieldSosText, sos.ID)
	if err != nil {
		return model.TrustedSosView{}, err
	}
	prof, err := s.profile(ctx, author)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return model.TrustedSosView{}, err
	}
	return model.TrustedSosView{Sos: sos, PlainText: text, OwnerDisplayName: prof.DisplayName, RespondedByMe: responded}, nil
}

// RespondAsTrustedContact records "I can help" from a confirmed trusted
// contact of the SOS author (respondAsTrustedContact), childcare included.
// It is the same response row as RespondToSos, so the author sees it in
// ListResponses and may accept it into a private chat; the first response
// notifies her exactly as RespondToSos does (template
// emergency.sos_responded). Access is checked like GetTrustedSos (404
// unknown, 403 not her contact) before the state: a closed SOS is
// model.ErrConflict unless the caller already responded (a retry of a
// recorded response returns nil). Idempotent per responder.
func (s *Service) RespondAsTrustedContact(ctx context.Context, p auth.Principal, sosID uuid.UUID) error {
	viewer, err := memberOf(p)
	if err != nil {
		return err
	}
	if _, err := s.sosAuthorFor(ctx, viewer, sosID); err != nil {
		return err
	}
	now := s.now()
	var created model.SosResponse
	var inserted bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		// Locked: a concurrent close by the author or a moderator waits, so
		// no response lands on an SOS that closed meanwhile.
		sos, err := trustedSos(ctx, tx, viewer, sosID, true)
		if err != nil {
			return err
		}
		already, err := tx.SosRespondedBy(ctx, sosID, viewer)
		if err != nil || already {
			return err
		}
		if sos.Status != model.SosOpen {
			return model.ErrConflict
		}
		created, inserted, err = tx.InsertSosResponse(ctx, sosID, viewer, now)
		if err != nil || !inserted {
			return err
		}
		return tx.Publish(ctx, now, model.Delivery(created.ID, model.MemberRecipient(sos.MemberID),
			model.ClassNormal, model.TemplateSosResponded, map[string]string{"sos_id": sosID.String()}))
	})
	if err != nil {
		return err
	}
	if inserted {
		s.log().Info("sos response", "event", "SosResponded", "sos_id", sosID, "response_id", created.ID, "via", "trusted_contact")
	}
	return nil
}

// invitationFor returns the member-contact entry addressed to memberID,
// or model.ErrNotFound when it does not exist or names someone else, so an
// invitation's existence is not revealed to anyone but the invited member.
func invitationFor(ctx context.Context, tx port.Tx, memberID, contactID uuid.UUID) (model.TrustedContact, error) {
	c, err := tx.GetTrustedContact(ctx, contactID)
	if err != nil {
		return model.TrustedContact{}, err
	}
	if c.Kind != model.ContactMember || c.ContactMemberID == nil || *c.ContactMemberID != memberID {
		return model.TrustedContact{}, model.ErrNotFound
	}
	return c, nil
}

// GetTrustedInvitation returns an invitation (or confirmed entry) addressed
// to the caller, with the inviting member's first name. Anyone else, a
// removed entry, or an owner blocked either way is model.ErrNotFound (as
// for AcceptTrustedInvitation).
func (s *Service) GetTrustedInvitation(ctx context.Context, p auth.Principal, contactID uuid.UUID) (model.InvitationView, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.InvitationView{}, err
	}
	var c model.TrustedContact
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		c, err = invitationFor(ctx, tx, memberID, contactID)
		return err
	}); err != nil {
		return model.InvitationView{}, err
	}
	if !c.Active() {
		return model.InvitationView{}, model.ErrNotFound
	}
	b, err := s.blocked(ctx, c.OwnerID, memberID)
	if err != nil {
		return model.InvitationView{}, err
	}
	if b {
		return model.InvitationView{}, model.ErrNotFound
	}
	prof, err := s.profile(ctx, c.OwnerID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return model.InvitationView{}, err
	}
	return model.InvitationView{ContactID: c.ID, OwnerDisplayName: prof.DisplayName, Status: c.Status, InvitedAt: c.CreatedAt}, nil
}

// DeclineTrustedInvitation lets the invited member decline an invitation or
// stop being a confirmed trusted contact. The effect is the owner's
// RemoveTrustedContact: removed_at is set (the row is kept, AGENTS.md 6)
// and the entry's plan links are revoked in the same transaction, so the
// shared plan, the trusted SOS and notify's lookups refuse her from then on.
// Links minted concurrently are still dead: the link view re-checks that the
// contact is not removed (GetLivePlanLink). The owner is not notified and
// nothing records a reason. Only the invited member (anyone else is
// model.ErrNotFound); declining a removed entry again returns nil.
func (s *Service) DeclineTrustedInvitation(ctx context.Context, p auth.Principal, contactID uuid.UUID) error {
	memberID, err := memberOf(p)
	if err != nil {
		return err
	}
	now := s.now()
	var removed bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		c, err := invitationFor(ctx, tx, memberID, contactID)
		if err != nil {
			return err
		}
		// The owner id comes from the row itself, so the update's owner
		// guard matches; removed_at IS NULL in it makes a repeat a no-op.
		if removed, err = tx.RemoveTrustedContact(ctx, c.ID, c.OwnerID, now); err != nil {
			return err
		}
		_, err = tx.RevokeContactPlanLinks(ctx, c.ID, now)
		return err
	})
	if err != nil {
		return err
	}
	if removed {
		s.log().Info("trusted invitation declined", "event", "TrustedInvitationDeclined", "contact_id", contactID)
	}
	return nil
}
