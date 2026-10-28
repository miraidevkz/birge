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
)

// SetChildCard stores the caller's child card, sealed with her key
// (associated data emergency.child_cards.text|<member id>).
func (s *Service) SetChildCard(ctx context.Context, p auth.Principal, text string) error {
	memberID, err := memberOf(p)
	if err != nil {
		return err
	}
	if err := model.ValidateChildCard(text); err != nil {
		return err
	}
	sealed, err := s.seal(ctx, memberID, text, model.FieldChildCard, memberID)
	if err != nil {
		return err
	}
	now := s.now()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		return tx.UpsertChildCard(ctx, memberID, sealed, now)
	})
}

// GetPlan returns the caller's emergency plan: her child card ("" when she
// has none), her active trusted contacts (external phones masked) and when
// she last sent it.
func (s *Service) GetPlan(ctx context.Context, p auth.Principal) (model.Plan, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.Plan{}, err
	}
	var card *model.ChildCard
	var contacts []model.TrustedContact
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		c, err := tx.GetChildCard(ctx, memberID)
		switch {
		case err == nil:
			card = &c
		case !errors.Is(err, model.ErrNotFound):
			return err
		}
		contacts, err = tx.ListActiveTrustedContacts(ctx, memberID)
		return err
	}); err != nil {
		return model.Plan{}, err
	}
	out := model.Plan{Contacts: make([]model.ContactView, 0, len(contacts))}
	if card != nil {
		text, err := s.open(ctx, memberID, card.Text, model.FieldChildCard, memberID)
		if err != nil {
			return model.Plan{}, err
		}
		out.ChildCard, out.LastSentAt = text, card.LastSentAt
	}
	views, err := s.contactViews(ctx, contacts)
	if err != nil {
		return model.Plan{}, err
	}
	out.Contacts = views
	return out, nil
}

// contactViews builds display views: member contacts get their display name
// from membership, external ones their opened name and a masked phone.
func (s *Service) contactViews(ctx context.Context, contacts []model.TrustedContact) ([]model.ContactView, error) {
	var memberIDs []uuid.UUID
	for _, c := range contacts {
		if c.Kind == model.ContactMember && c.ContactMemberID != nil {
			memberIDs = append(memberIDs, *c.ContactMemberID)
		}
	}
	profs, err := s.profiles(ctx, memberIDs...)
	if err != nil {
		return nil, err
	}
	out := make([]model.ContactView, 0, len(contacts))
	for _, c := range contacts {
		v, err := s.contactView(ctx, c, profs)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) contactView(ctx context.Context, c model.TrustedContact, profs map[uuid.UUID]model.Profile) (model.ContactView, error) {
	v := model.ContactView{ID: c.ID, Kind: c.Kind, Status: c.Status}
	if c.Kind == model.ContactMember {
		if c.ContactMemberID != nil {
			v.DisplayName = profs[*c.ContactMemberID].DisplayName
		}
		return v, nil
	}
	name, err := s.open(ctx, c.OwnerID, c.Name, model.FieldContactName, c.ID)
	if err != nil {
		return model.ContactView{}, err
	}
	phone, err := s.open(ctx, c.OwnerID, c.Phone, model.FieldContactPhone, c.ID)
	if err != nil {
		return model.ContactView{}, err
	}
	v.DisplayName, v.PhoneMasked = name, model.MaskPhone(phone)
	return v, nil
}

// AddTrustedContact adds a trusted contact to the caller's plan. A repeated
// Idempotency-Key returns the contact added with it.
//
// Kind member: the member must have met the caller in person
// (Acquaintances.Met, § 3; model.ErrForbidden otherwise, also when either
// blocked the other) and must accept (status invited until
// AcceptTrustedInvitation); she is notified with template
// emergency.trusted_invitation. Kind external: name and phone are sealed
// with the caller's key and the contact is confirmed at once; responses
// carry only a masked phone.
//
// At most model.MaxTrustedContacts active (invited + confirmed) contacts,
// checked under a per-owner lock; more, or a member who already is an
// active contact, is model.ErrConflict.
func (s *Service) AddTrustedContact(ctx context.Context, p auth.Principal, idempotencyKey uuid.UUID, in model.NewTrustedContact) (model.ContactView, error) {
	owner, err := memberOf(p)
	if err != nil {
		return model.ContactView{}, err
	}
	if idempotencyKey == uuid.Nil {
		return model.ContactView{}, model.ErrInvalidInput
	}
	if c, found, err := s.contactByKey(ctx, owner, idempotencyKey); err != nil || found {
		return c, err
	}
	id := uuid.Must(uuid.NewV7())
	now := s.now()
	c := model.TrustedContact{
		ID: id, OwnerID: owner, IdempotencyKey: idempotencyKey, Kind: in.Kind, CreatedAt: now,
	}
	ownerProf, err := s.profile(ctx, owner)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return model.ContactView{}, err
	}
	c.Lang = model.LangOrDefault(ownerProf.Lang)
	switch in.Kind {
	case model.ContactMember:
		if err := s.checkMemberContact(ctx, owner, in.MemberID); err != nil {
			return model.ContactView{}, err
		}
		mid := in.MemberID
		c.ContactMemberID, c.Status = &mid, model.ContactInvited
	case model.ContactExternal:
		if err := model.ValidateContactName(in.Name); err != nil {
			return model.ContactView{}, err
		}
		if !model.PhoneIsE164(in.Phone) {
			return model.ContactView{}, fmt.Errorf("%w: phone", model.ErrInvalidInput)
		}
		if c.Name, err = s.seal(ctx, owner, in.Name, model.FieldContactName, id); err != nil {
			return model.ContactView{}, err
		}
		if c.Phone, err = s.seal(ctx, owner, in.Phone, model.FieldContactPhone, id); err != nil {
			return model.ContactView{}, err
		}
		c.Status, c.ConfirmedAt = model.ContactConfirmed, &now
	default:
		return model.ContactView{}, fmt.Errorf("%w: kind", model.ErrInvalidInput)
	}
	var out model.TrustedContact
	var added bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		if err := tx.Lock(ctx, model.LockContacts, owner); err != nil {
			return err
		}
		if existing, err := tx.GetTrustedContactByIdempotencyKey(ctx, owner, idempotencyKey); err == nil {
			out = existing
			return nil
		} else if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		active, err := tx.ListActiveTrustedContacts(ctx, owner)
		if err != nil {
			return err
		}
		if len(active) >= model.MaxTrustedContacts {
			return fmt.Errorf("%w: at most %d trusted contacts", model.ErrConflict, model.MaxTrustedContacts)
		}
		created, inserted, err := tx.InsertTrustedContact(ctx, c)
		if err != nil {
			return err
		}
		if !inserted {
			// The member already is an active contact of the owner.
			return fmt.Errorf("%w: already a trusted contact", model.ErrConflict)
		}
		out, added = created, true
		if c.Kind != model.ContactMember {
			return nil
		}
		return tx.Publish(ctx, now, model.Delivery(id, model.MemberRecipient(in.MemberID), model.ClassNormal,
			model.TemplateTrustedInvitation, map[string]string{"contact_id": id.String()}))
	})
	if err != nil {
		return model.ContactView{}, err
	}
	if added {
		s.log().Info("trusted contact added", "event", "TrustedContactAdded", "contact_id", out.ID, "kind", out.Kind)
	}
	views, err := s.contactViews(ctx, []model.TrustedContact{out})
	if err != nil {
		return model.ContactView{}, err
	}
	return views[0], nil
}

// contactByKey returns the view of the owner's contact added with key, if any.
func (s *Service) contactByKey(ctx context.Context, owner, key uuid.UUID) (model.ContactView, bool, error) {
	var c model.TrustedContact
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		c, err = tx.GetTrustedContactByIdempotencyKey(ctx, owner, key)
		return err
	})
	if errors.Is(err, model.ErrNotFound) {
		return model.ContactView{}, false, nil
	}
	if err != nil {
		return model.ContactView{}, false, err
	}
	views, err := s.contactViews(ctx, []model.TrustedContact{c})
	if err != nil {
		return model.ContactView{}, false, err
	}
	return views[0], true, nil
}

// checkMemberContact enforces who may be invited as a member contact: an
// existing member other than the owner, not blocked either way, whom the
// owner met in person (§ 3, N5).
func (s *Service) checkMemberContact(ctx context.Context, owner, member uuid.UUID) error {
	if member == uuid.Nil || member == owner {
		return fmt.Errorf("%w: member id", model.ErrInvalidInput)
	}
	met, err := s.met(ctx, owner, member)
	if err != nil {
		return err
	}
	if !met {
		return fmt.Errorf("%w: not met in person", model.ErrForbidden)
	}
	b, err := s.blocked(ctx, owner, member)
	if err != nil {
		return err
	}
	if b {
		return model.ErrForbidden
	}
	if _, err := s.profile(ctx, member); err != nil {
		return err
	}
	return nil
}

// RemoveTrustedContact removes one of the caller's contacts. The row is kept
// with removed_at (no deletion path, AGENTS.md 6); notify's lookups return
// ErrNotFound for it from then on and her external plan links are revoked
// in the same transaction. Removing twice is fine; another member's
// contact is model.ErrNotFound.
func (s *Service) RemoveTrustedContact(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	owner, err := memberOf(p)
	if err != nil {
		return err
	}
	now := s.now()
	var removed bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		c, err := tx.GetTrustedContact(ctx, id)
		if err != nil {
			return err
		}
		if c.OwnerID != owner {
			return model.ErrNotFound
		}
		if removed, err = tx.RemoveTrustedContact(ctx, id, owner, now); err != nil {
			return err
		}
		// Her SMS link dies with the contact (contract: removal revokes).
		_, err = tx.RevokeContactPlanLinks(ctx, id, now)
		return err
	})
	if err != nil {
		return err
	}
	if removed {
		s.log().Info("trusted contact removed", "event", "TrustedContactRemoved", "contact_id", id)
	}
	return nil
}

// AcceptTrustedInvitation confirms an invitation addressed to the caller.
// Only the invited member may accept (anyone else, or a removed invitation,
// is model.ErrNotFound). "Met in person" is checked again, because it is the
// condition for being a trusted contact (§ 3). Accepting twice is fine.
func (s *Service) AcceptTrustedInvitation(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	memberID, err := memberOf(p)
	if err != nil {
		return err
	}
	var c model.TrustedContact
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		c, err = tx.GetTrustedContact(ctx, id)
		return err
	}); err != nil {
		return err
	}
	if !c.Active() || c.Kind != model.ContactMember || c.ContactMemberID == nil || *c.ContactMemberID != memberID {
		return model.ErrNotFound
	}
	if c.Status == model.ContactConfirmed {
		return nil
	}
	met, err := s.met(ctx, c.OwnerID, memberID)
	if err != nil {
		return err
	}
	if !met {
		return fmt.Errorf("%w: not met in person", model.ErrForbidden)
	}
	b, err := s.blocked(ctx, c.OwnerID, memberID)
	if err != nil {
		return err
	}
	if b {
		return model.ErrNotFound
	}
	now := s.now()
	var confirmed bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		if _, confirmed, err = tx.ConfirmTrustedContact(ctx, id, memberID, now); err != nil || confirmed {
			return err
		}
		cur, err := tx.GetTrustedContact(ctx, id)
		if err != nil {
			return err
		}
		if cur.Confirmed() {
			return nil // accepted concurrently
		}
		return model.ErrNotFound // removed meanwhile
	})
	if err != nil {
		return err
	}
	if confirmed {
		s.log().Info("trusted contact confirmed", "event", "TrustedContactConfirmed", "contact_id", id)
	}
	return nil
}

// SendPlan sends the caller's child card to her confirmed trusted contacts
// after the explicit confirmation "SEND": one notify.DeliveryRequested per
// confirmed contact (class normal, template emergency.plan_sent, params
// {owner_id}), emergency.PlanSent, and the send time on the card and on
// each contact, in one transaction; earlier external plan links are revoked
// in it. An external contact's SMS carries a link minted at send time
// (ExternalPlanLink), never one stored in the event. A repeated Idempotency-Key does nothing.
// No card or no confirmed contact is model.ErrConflict; more than
// model.MaxPlanSendsPerHour sends per hour is model.ErrRateLimited (external
// contacts get an SMS).
func (s *Service) SendPlan(ctx context.Context, p auth.Principal, idempotencyKey uuid.UUID, confirm string) error {
	memberID, err := memberOf(p)
	if err != nil {
		return err
	}
	if idempotencyKey == uuid.Nil || confirm != "SEND" {
		return model.ErrInvalidInput
	}
	now := s.now()
	sendID := uuid.Must(uuid.NewV7())
	var sent int
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		if err := tx.Lock(ctx, model.LockPlan, memberID); err != nil {
			return err
		}
		if _, err := tx.GetPlanSendByIdempotencyKey(ctx, memberID, idempotencyKey); err == nil {
			return nil
		} else if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		n, err := tx.CountPlanSendsSince(ctx, memberID, now.Add(-time.Hour))
		if err != nil {
			return err
		}
		if n >= model.MaxPlanSendsPerHour {
			return model.ErrRateLimited
		}
		if _, err := tx.GetChildCard(ctx, memberID); errors.Is(err, model.ErrNotFound) {
			return fmt.Errorf("%w: no child card", model.ErrConflict)
		} else if err != nil {
			return err
		}
		all, err := tx.ListActiveTrustedContacts(ctx, memberID)
		if err != nil {
			return err
		}
		var confirmed []model.TrustedContact
		for _, c := range all {
			if c.Confirmed() {
				confirmed = append(confirmed, c)
			}
		}
		if len(confirmed) == 0 {
			return fmt.Errorf("%w: no confirmed trusted contact", model.ErrConflict)
		}
		if _, inserted, err := tx.InsertPlanSend(ctx, model.PlanSend{
			ID: sendID, MemberID: memberID, IdempotencyKey: idempotencyKey, ContactCount: len(confirmed), SentAt: now,
		}); err != nil || !inserted {
			return err
		}
		// A new send revokes the links of the previous one; the new links
		// are minted when notify sends each SMS (ExternalPlanLink).
		if _, err := tx.RevokeOwnerPlanLinks(ctx, memberID, now); err != nil {
			return err
		}
		if _, err := tx.MarkContactsPlanSent(ctx, memberID, now); err != nil {
			return err
		}
		if err := tx.MarkChildCardSent(ctx, memberID, now); err != nil {
			return err
		}
		if err := tx.Publish(ctx, now, model.Event{
			Type: model.EventPlanSent, Version: model.EventVersion, Data: model.PlanSentData{MemberID: memberID},
		}); err != nil {
			return err
		}
		params := map[string]string{"owner_id": memberID.String()}
		for _, c := range confirmed {
			if err := tx.Publish(ctx, now, model.Delivery(sendID, model.ContactRecipient(c), model.ClassNormal,
				model.TemplatePlanSent, params)); err != nil {
				return err
			}
		}
		sent = len(confirmed)
		return nil
	})
	if err != nil {
		if errors.Is(err, model.ErrRateLimited) {
			s.log().Info("plan send rate limited", "event", "PlanSendRateLimited")
		}
		return err
	}
	if sent > 0 {
		s.log().Info("plan sent", "event", model.EventPlanSent, "plan_send_id", sendID, "contacts", sent)
	}
	return nil
}

// GetSharedPlan returns ownerID's child card to the caller, only while the
// caller is an active, confirmed member contact of that owner and only after
// the owner sent her the plan (model.ErrForbidden otherwise, without saying
// which condition failed). The audit entry emergency_plan.view_shared is
// committed before the card is opened. The API answers with
// Cache-Control: no-store. The card shown is the owner's current card.
func (s *Service) GetSharedPlan(ctx context.Context, p auth.Principal, ownerID uuid.UUID) (model.SharedPlan, error) {
	viewer, err := memberOf(p)
	if err != nil {
		return model.SharedPlan{}, err
	}
	if ownerID == uuid.Nil || ownerID == viewer {
		return model.SharedPlan{}, model.ErrForbidden
	}
	now := s.now()
	var card model.ChildCard
	var sentAt time.Time
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		c, err := tx.GetSharedPlanContact(ctx, ownerID, viewer)
		if errors.Is(err, model.ErrNotFound) {
			return model.ErrForbidden
		}
		if err != nil {
			return err
		}
		if c.PlanSentAt == nil {
			return model.ErrForbidden
		}
		sentAt = *c.PlanSentAt
		if card, err = tx.GetChildCard(ctx, ownerID); errors.Is(err, model.ErrNotFound) {
			return model.ErrForbidden
		} else if err != nil {
			return err
		}
		return tx.Audit(ctx, model.AuditEntry{
			At: now, ActorID: viewer, ActorRole: model.AuditActorMember, Action: model.AuditViewSharedPlan,
			ObjectType: model.AuditObjectChildCard, ObjectID: ownerID,
		})
	})
	if err != nil {
		return model.SharedPlan{}, err
	}
	s.log().Info("shared plan viewed", "event", "SharedPlanViewed")
	text, err := s.open(ctx, ownerID, card.Text, model.FieldChildCard, ownerID)
	if err != nil {
		return model.SharedPlan{}, err
	}
	prof, err := s.profile(ctx, ownerID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return model.SharedPlan{}, err
	}
	return model.SharedPlan{OwnerDisplayName: prof.DisplayName, ChildCard: text, SentAt: sentAt}, nil
}
