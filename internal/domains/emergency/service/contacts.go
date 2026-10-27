package service

import (
	"context"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
)

// ContactPhone returns the phone (E.164) and SMS language of an external
// trusted contact, for notify to send an SMS at send time. The phone is
// opened with the owner's key and must not be stored or logged by the
// caller. ErrNotFound when the contact is unknown, removed or a member
// contact (notify reaches those by member_id).
func (s *Service) ContactPhone(ctx context.Context, trustedContactID uuid.UUID) (phone, lang string, err error) {
	var c model.TrustedContact
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		c, err = tx.GetTrustedContact(ctx, trustedContactID)
		return err
	}); err != nil {
		return "", "", err
	}
	if !c.Active() || c.Kind != model.ContactExternal {
		return "", "", ErrNotFound
	}
	phone, err = s.open(ctx, c.OwnerID, c.Phone, model.FieldContactPhone, c.ID)
	if err != nil {
		return "", "", err
	}
	return phone, model.LangOrDefault(c.Lang), nil
}

// TrustedContactTarget tells notify what a trusted_contact_id stands for: a
// member (memberID set, external false) or an external contact (memberID
// nil, external true, phone via ContactPhone). Only confirmed, active
// contacts resolve; unknown, removed or still-invited ones are ErrNotFound.
func (s *Service) TrustedContactTarget(ctx context.Context, id uuid.UUID) (memberID *uuid.UUID, external bool, err error) {
	var c model.TrustedContact
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		c, err = tx.GetTrustedContact(ctx, id)
		return err
	}); err != nil {
		return nil, false, err
	}
	if !c.Confirmed() {
		return nil, false, ErrNotFound
	}
	if c.Kind == model.ContactExternal {
		return nil, true, nil
	}
	if c.ContactMemberID == nil {
		return nil, false, ErrNotFound
	}
	m := *c.ContactMemberID
	return &m, false, nil
}
