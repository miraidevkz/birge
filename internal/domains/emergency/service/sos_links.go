package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
)

// sosLinkHashContext is prepended to an SOS link token before it is hashed
// under the link key. Plan links hash the bare token, so a plan token can
// never match an SOS link hash and an SOS token never a plan link hash, even
// if the tables were mixed up: each kind opens only its own page. It is part
// of the stored hashes; changing it kills every live SOS link.
const sosLinkHashContext = "emergency.sos_link|"

func (s *Service) hashSosLinkToken(token string) ([]byte, error) {
	return s.hashLinkToken(sosLinkHashContext, token)
}

// ExternalSosLink mints the link token of an external trusted contact for
// one SOS, for notify to put into the emergency.sos_raised_contact SMS at
// send time (through wiring's SMS links adapter). Like ExternalPlanLink it
// is minted then, so the token exists only in this return value and in the
// SMS, never in the outbox, the event or notify's tables. The caller must
// not log or store it.
//
// The contact must be active, external and confirmed and belong to the SOS
// author; the SOS must be open and before its auto-close and less than
// model.SosLinkMaxTTL old. Anything else is ErrNotFound: the SMS then goes
// out without the link (notify's fallback for critical templates), never
// not at all. The link expires when the SOS auto-closes or 24 hours after it
// was raised, whichever is first; closing it earlier ends the link too,
// because the view re-checks the SOS. Minting revokes the contact's earlier
// link for the same SOS (a retried SMS leaves one working link) and
// schedules the retention purge, in one transaction.
func (s *Service) ExternalSosLink(ctx context.Context, trustedContactID, sosID uuid.UUID) (string, error) {
	if trustedContactID == uuid.Nil || sosID == uuid.Nil {
		return "", model.ErrNotFound
	}
	token, err := newLinkToken()
	if err != nil {
		return "", fmt.Errorf("emergency: sos link token: %w", err)
	}
	hash, err := s.hashSosLinkToken(token)
	if err != nil {
		return "", err
	}
	now := s.now()
	linkID := uuid.Must(uuid.NewV7())
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		if err := tx.Lock(ctx, model.LockSosLink, trustedContactID); err != nil {
			return err
		}
		c, err := tx.GetTrustedContact(ctx, trustedContactID)
		if err != nil {
			return err
		}
		if !c.Confirmed() || c.Kind != model.ContactExternal {
			return model.ErrNotFound
		}
		sos, err := tx.GetSos(ctx, sosID, false)
		if err != nil {
			return err
		}
		if sos.MemberID != c.OwnerID || sos.Status != model.SosOpen {
			return model.ErrNotFound
		}
		expires := sos.CreatedAt.Add(model.SosLinkMaxTTL)
		if sos.ClosesAt.Before(expires) {
			expires = sos.ClosesAt
		}
		if !now.Before(expires) {
			return model.ErrNotFound
		}
		if _, err := tx.RevokeSosContactLinks(ctx, sosID, c.ID, now); err != nil {
			return err
		}
		if err := tx.InsertSosLink(ctx, model.SosLink{
			ID: linkID, TokenHash: hash, SosID: sosID, ContactID: c.ID, OwnerID: c.OwnerID,
			CreatedAt: now, ExpiresAt: expires,
		}); err != nil {
			return err
		}
		_, err = tx.ScheduleSosLinkPurge(ctx, linkID, expires.Add(model.SosLinkRetention))
		return err
	})
	if err != nil {
		return "", err
	}
	s.log().Info("sos link minted", "event", "SosLinkMinted", "sos_link_id", linkID, "sos_id", sosID, "contact_id", trustedContactID)
	return token, nil
}

// ViewExternalSos returns the SOS behind an SMS link (no session). Unknown,
// expired and revoked tokens, a closed SOS, a removed contact and a
// malformed token are all model.ErrNotFound: one keyed-hash lookup decides
// (port.Tx.GetLiveSosLink), so neither the status nor the number of queries
// tells the cases apart. The audit entry emergency_sos.view_external (actor
// = the contact id, role external_contact, object = the SOS) is committed
// before the text is opened; it never carries the token or the client IP.
// The API answers with Cache-Control: no-store; the per-IP rate limit is
// applied in front of the handler.
//
// Every SOS type is shown, childcare included: an external trusted contact
// is one of the mother's own trusted contacts (N5 allows exactly them).
func (s *Service) ViewExternalSos(ctx context.Context, token string) (model.ExternalSos, error) {
	if !planLinkTokenRe.MatchString(token) {
		return model.ExternalSos{}, model.ErrNotFound
	}
	hash, err := s.hashSosLinkToken(token)
	if err != nil {
		return model.ExternalSos{}, err
	}
	now := s.now()
	var live model.LiveSosLink
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		if live, err = tx.GetLiveSosLink(ctx, hash, now); err != nil {
			return err
		}
		return tx.Audit(ctx, model.AuditEntry{
			At: now, ActorID: live.Link.ContactID, ActorRole: model.AuditActorExternalContact,
			Action: model.AuditViewExternalSos, ObjectType: model.AuditObjectSos, ObjectID: live.Link.SosID,
		})
	})
	if err != nil {
		return model.ExternalSos{}, err
	}
	s.log().Info("external sos viewed", "event", "ExternalSosViewed", "sos_link_id", live.Link.ID, "sos_id", live.Link.SosID, "contact_id", live.Link.ContactID)
	sos := live.Sos
	text, err := s.open(ctx, sos.MemberID, sos.Text, model.FieldSosText, sos.ID)
	if err != nil {
		return model.ExternalSos{}, err
	}
	prof, err := s.profile(ctx, sos.MemberID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return model.ExternalSos{}, err
	}
	return model.ExternalSos{
		OwnerDisplayName: prof.DisplayName, Type: sos.Type, Text: text, Status: sos.Status,
		CreatedAt: sos.CreatedAt, ClosesAt: sos.ClosesAt,
	}, nil
}

// HandleSosLinkPurge runs the emergency.SosLinkPurge timer in the worker's
// transaction: it deletes the link once it has been expired for
// model.SosLinkRetention (ADR-0023). A link already deleted (account
// deletion) or, by clock skew, not yet due is left alone.
func (s *Service) HandleSosLinkPurge(ctx context.Context, tx port.Tx, linkID uuid.UUID) error {
	n, err := tx.DeleteExpiredSosLink(ctx, linkID, s.now().Add(-model.SosLinkRetention))
	if err != nil {
		return err
	}
	if n > 0 {
		s.log().Info("sos link purged", "event", "SosLinkPurged", "sos_link_id", linkID)
	}
	return nil
}
