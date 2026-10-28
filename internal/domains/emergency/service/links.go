package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
)

// planLinkTokenRe is the contract's token shape (43 characters of unpadded
// base64url = 32 bytes).
var planLinkTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// errPlanLinkKey is returned when Settings.PlanLinkKey is missing; the
// wiring refuses to start without BIRGE_PLAN_LINK_KEY_FILE, so this only
// guards a misconfigured test or tool.
var errPlanLinkKey = errors.New("emergency: plan link key not configured")

// hashPlanLinkToken is the only form in which a token reaches the database:
// HMAC-SHA256 under the deployment's plan link key. A keyed hash (not plain
// SHA-256) means a stolen dump cannot be checked against guessed tokens
// without the key; the token's 256 bits already rule out guessing.
func (s *Service) hashPlanLinkToken(token string) ([]byte, error) {
	if len(s.Settings.PlanLinkKey) == 0 {
		return nil, errPlanLinkKey
	}
	m := hmac.New(sha256.New, s.Settings.PlanLinkKey)
	m.Write([]byte(token))
	return m.Sum(nil), nil
}

// ExternalPlanLink mints the one-time link token of an external trusted
// contact, for notify to put into the emergency.plan_sent SMS at send time.
// It is minted then, not in SendPlan, so the token never sits in clear in
// the outbox, the event or notify's tables: it exists only in this return
// value and in the SMS. The caller must not log or store it.
//
// The contact must be active, external and confirmed and have been sent the
// plan less than model.PlanLinkTTL ago (ErrNotFound otherwise: the SMS is
// then skipped). The link expires model.PlanLinkTTL after that send, so a
// delayed or retried SMS does not stretch the 24 hours. Minting revokes the
// contact's earlier links (a retried SMS leaves exactly one working link) and
// schedules the retention purge, in one transaction.
func (s *Service) ExternalPlanLink(ctx context.Context, trustedContactID uuid.UUID) (string, error) {
	raw := make([]byte, model.PlanLinkTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("emergency: plan link token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash, err := s.hashPlanLinkToken(token)
	if err != nil {
		return "", err
	}
	now := s.now()
	linkID := uuid.Must(uuid.NewV7())
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		if err := tx.Lock(ctx, model.LockPlanLink, trustedContactID); err != nil {
			return err
		}
		c, err := tx.GetTrustedContact(ctx, trustedContactID)
		if err != nil {
			return err
		}
		if !c.Confirmed() || c.Kind != model.ContactExternal || c.PlanSentAt == nil {
			return model.ErrNotFound
		}
		expires := c.PlanSentAt.Add(model.PlanLinkTTL)
		if !now.Before(expires) {
			return model.ErrNotFound
		}
		if _, err := tx.RevokeContactPlanLinks(ctx, c.ID, now); err != nil {
			return err
		}
		if err := tx.InsertPlanLink(ctx, model.PlanLink{
			ID: linkID, TokenHash: hash, ContactID: c.ID, OwnerID: c.OwnerID,
			SentAt: *c.PlanSentAt, CreatedAt: now, ExpiresAt: expires,
		}); err != nil {
			return err
		}
		_, err = tx.SchedulePlanLinkPurge(ctx, linkID, expires.Add(model.PlanLinkRetention))
		return err
	})
	if err != nil {
		return "", err
	}
	s.log().Info("plan link minted", "event", "PlanLinkMinted", "plan_link_id", linkID, "contact_id", trustedContactID)
	return token, nil
}

// ViewExternalPlan returns the child card behind an SMS link (no session).
// Unknown, expired and revoked tokens, and a malformed one, are all
// model.ErrNotFound: one keyed-hash lookup decides, so neither the status nor
// the number of queries tells the cases apart. The audit entry
// emergency_plan.view_external (actor = the contact id, role
// external_contact, object = the owner's child card) is committed before the
// card is opened; it never carries the token or the client IP. The API
// answers with Cache-Control: no-store; the per-IP rate limit is applied in
// front of the handler.
func (s *Service) ViewExternalPlan(ctx context.Context, token string) (model.SharedPlan, error) {
	if !planLinkTokenRe.MatchString(token) {
		return model.SharedPlan{}, model.ErrNotFound
	}
	hash, err := s.hashPlanLinkToken(token)
	if err != nil {
		return model.SharedPlan{}, err
	}
	now := s.now()
	var link model.PlanLink
	var card model.ChildCard
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		if link, err = tx.GetLivePlanLink(ctx, hash, now); err != nil {
			return err
		}
		if card, err = tx.GetChildCard(ctx, link.OwnerID); err != nil {
			return err
		}
		return tx.Audit(ctx, model.AuditEntry{
			At: now, ActorID: link.ContactID, ActorRole: model.AuditActorExternalContact,
			Action: model.AuditViewExternalPlan, ObjectType: model.AuditObjectChildCard, ObjectID: link.OwnerID,
		})
	})
	if err != nil {
		return model.SharedPlan{}, err
	}
	s.log().Info("external plan viewed", "event", "ExternalPlanViewed", "plan_link_id", link.ID, "contact_id", link.ContactID)
	text, err := s.open(ctx, link.OwnerID, card.Text, model.FieldChildCard, link.OwnerID)
	if err != nil {
		return model.SharedPlan{}, err
	}
	prof, err := s.profile(ctx, link.OwnerID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return model.SharedPlan{}, err
	}
	return model.SharedPlan{OwnerDisplayName: prof.DisplayName, ChildCard: text, SentAt: link.SentAt}, nil
}

// HandlePlanLinkPurge runs the emergency.PlanLinkPurge timer in the worker's
// transaction: it deletes the link once it has been expired for
// model.PlanLinkRetention (ADR-0023). A link already deleted (account
// deletion) or, by clock skew, not yet due is left alone.
func (s *Service) HandlePlanLinkPurge(ctx context.Context, tx port.Tx, linkID uuid.UUID) error {
	n, err := tx.DeleteExpiredPlanLink(ctx, linkID, s.now().Add(-model.PlanLinkRetention))
	if err != nil {
		return err
	}
	if n > 0 {
		s.log().Info("plan link purged", "event", "PlanLinkPurged", "plan_link_id", linkID)
	}
	return nil
}
