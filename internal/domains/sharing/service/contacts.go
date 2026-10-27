package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
	"birge/internal/platform/auth"
)

// RequestContact asks the listing author to talk. Idempotent per listing and
// requester. Notifies the author (template sharing.contact_request).
func (s *Service) RequestContact(ctx context.Context, p auth.Principal, listingID uuid.UUID) error {
	requester, err := memberOf(p)
	if err != nil {
		return err
	}
	now := s.now()
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("sharing: contact request id: %w", err)
	}

	return s.Store.InTx(ctx, func(tx port.Tx) error {
		l, err := tx.GetListing(ctx, listingID, false)
		if err != nil {
			return err
		}
		if l.Status != model.ListingActive {
			return model.ErrNotFound
		}
		if l.AuthorID == requester {
			return model.ErrConflict
		}
		b, err := s.blocked(ctx, requester, l.AuthorID)
		if err != nil {
			return err
		}
		if b {
			return model.ErrNotFound
		}
		created, inserted, err := tx.InsertContactRequest(ctx, model.ContactRequest{
			ID: id, ListingID: listingID, RequesterID: requester,
			Status: model.ContactPending, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !inserted {
			return nil
		}
		if err := tx.Publish(ctx, now, model.Delivery(
			created.ID, l.AuthorID, model.TemplateContactRequest,
			map[string]string{"contact_request_id": created.ID.String()},
		)); err != nil {
			return err
		}
		s.log().Info("contact request created", "event", "ContactRequestCreated", "contact_request_id", created.ID)
		return nil
	})
}

// AcceptContact accepts a contact request on one of the caller's listings and
// emits sharing.ContactAccepted (chat opens the room). Idempotent.
func (s *Service) AcceptContact(ctx context.Context, p auth.Principal, contactRequestID uuid.UUID) error {
	author, err := memberOf(p)
	if err != nil {
		return err
	}
	now := s.now()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		c, err := tx.GetContactRequest(ctx, contactRequestID, true)
		if err != nil {
			return err
		}
		l, err := tx.GetListing(ctx, c.ListingID, false)
		if err != nil {
			return err
		}
		if l.AuthorID != author {
			return model.ErrNotFound
		}
		if c.Status == model.ContactAccepted {
			return nil
		}
		b, err := s.blocked(ctx, author, c.RequesterID)
		if err != nil {
			return err
		}
		if b {
			return model.ErrNotFound
		}
		accepted, updated, err := tx.AcceptContactRequest(ctx, contactRequestID, now)
		if err != nil {
			return err
		}
		if !updated {
			return nil
		}
		if err := tx.Publish(ctx, now, model.Event{
			Type:    model.EventContactAccepted,
			Version: model.EventVersion,
			Data: model.ContactAcceptedData{
				ListingID: accepted.ListingID, AuthorID: author, RequesterID: accepted.RequesterID,
			},
		}); err != nil {
			return err
		}
		s.log().Info("contact accepted", "event", "ContactAccepted", "contact_request_id", accepted.ID)
		return nil
	})
}
