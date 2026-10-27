package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/port"
	"birge/internal/platform/outbox"
)

// memberVerified is the payload of membership.MemberVerified v1.
// verified_by is the moderation item ID when applied from moderation.Decided
// (that event carries no staff identity).
type memberVerified struct {
	MemberID   uuid.UUID `json:"member_id"`
	VerifiedBy uuid.UUID `json:"verified_by"`
}

// ApplyModerationDecided applies moderation.Decided for kind verification.
// Deduplicated via platform.processed_events. Approve → member verified and
// membership.MemberVerified in the same transaction; reject → request
// rejected with the reason code. Other kinds are ignored (chat applies
// chat_message/report; block_author is SuspendAuthor).
func (s *Service) ApplyModerationDecided(ctx context.Context, eventID uuid.UUID, data model.ModerationDecidedData) error {
	if data.Kind != model.ModerationKindVerification {
		return nil
	}
	if eventID == uuid.Nil || data.ObjectID == uuid.Nil {
		return model.ErrInvalidRequest
	}
	now := s.clock.Now().UTC()
	return s.store.InTx(ctx, func(tx port.Tx) error {
		newly, err := tx.MarkProcessed(ctx, model.ConsumerModerationDecided, eventID)
		if err != nil {
			return err
		}
		if !newly {
			return nil
		}
		req, err := tx.GetVerificationRequest(ctx, data.ObjectID, true)
		if err != nil {
			if errors.Is(err, model.ErrMemberNotFound) {
				return nil
			}
			return err
		}
		if req.Status != model.VerificationRequestPending {
			return nil
		}
		if data.Approve {
			updated, err := tx.DecideVerificationRequest(ctx, req.ID, model.VerificationRequestApproved, "", now)
			if err != nil || !updated {
				return err
			}
			if err := tx.SetVerification(ctx, req.MemberID, model.VerificationVerified, now); err != nil {
				return err
			}
			if err := tx.Audit(ctx, data.ItemID, model.ActorRoleSystem, model.AuditVerifyApprove,
				model.AuditObjectVerification, req.ID, ""); err != nil {
				return err
			}
			return tx.Publish(ctx, now, outbox.Event{
				Type: model.EventMemberVerified, Version: 1,
				Data: memberVerified{MemberID: req.MemberID, VerifiedBy: data.ItemID},
			})
		}
		reason := data.Reason
		if reason == "" {
			reason = "other"
		}
		if utf8.RuneCountInString(reason) > 40 {
			reason = string([]rune(reason)[:40])
		}
		updated, err := tx.DecideVerificationRequest(ctx, req.ID, model.VerificationRequestRejected, reason, now)
		if err != nil || !updated {
			return err
		}
		// A rejected call leaves her able to request again.
		if err := tx.SetVerification(ctx, req.MemberID, model.VerificationNew, now); err != nil {
			return err
		}
		return tx.Audit(ctx, data.ItemID, model.ActorRoleSystem, model.AuditVerifyReject,
			model.AuditObjectVerification, req.ID, reason)
	})
}

// SuspendAuthor suspends a member after moderation.Decided with block_author
// (called from chat). Idempotent when already suspended. Audited as system
// with the moderation item ID. reason must be a short code.
func (s *Service) SuspendAuthor(ctx context.Context, memberID uuid.UUID, reason string, moderationItemID uuid.UUID) error {
	if memberID == uuid.Nil || moderationItemID == uuid.Nil {
		return model.ErrInvalidRequest
	}
	if reason == "" {
		reason = "other"
	}
	if utf8.RuneCountInString(reason) > 40 {
		reason = string([]rune(reason)[:40])
	}
	now := s.clock.Now().UTC()
	return s.store.InTx(ctx, func(tx port.Tx) error {
		rec, err := tx.MemberByID(ctx, memberID)
		if errors.Is(err, model.ErrMemberNotFound) {
			// Unknown or deleted (account deletion): nothing to suspend. A
			// no-op, not an error, or chat would redeliver the decision forever.
			return nil
		}
		if err != nil {
			return err
		}
		if rec.SuspendedAt != nil {
			return nil
		}
		if err := tx.SetSuspension(ctx, memberID, &now, reason, now); err != nil {
			return err
		}
		return tx.Audit(ctx, moderationItemID, model.ActorRoleSystem, model.AuditSuspend,
			model.AuditObjectMember, memberID, reason)
	})
}

// VerificationSummary returns the first name and district id for a
// verification request (ContentSource). No contact data. Gone request →
// model.ErrMemberNotFound.
func (s *Service) VerificationSummary(ctx context.Context, requestID uuid.UUID) (string, error) {
	if requestID == uuid.Nil {
		return "", model.ErrMemberNotFound
	}
	var text string
	err := s.store.InTx(ctx, func(tx port.Tx) error {
		req, err := tx.GetVerificationRequest(ctx, requestID, false)
		if err != nil {
			return err
		}
		rec, err := tx.MemberByID(ctx, req.MemberID)
		if err != nil {
			return err
		}
		name, err := s.open(ctx, rec.ID, rec.SealedName)
		if err != nil {
			return err
		}
		first := strings.TrimSpace(name)
		if i := strings.IndexAny(first, " \t"); i >= 0 {
			first = first[:i]
		}
		district := ""
		if rec.DistrictID != nil {
			district = rec.DistrictID.String()
		}
		switch {
		case first == "":
			text = district
		case district == "":
			text = first
		default:
			text = first + " · " + district
		}
		return nil
	})
	return text, err
}
