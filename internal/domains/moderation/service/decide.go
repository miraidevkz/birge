package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/port"
	"birge/internal/platform/auth"
)

// Decide records a decision on an open item. In one transaction it locks the
// item row, validates d against the kind, marks the item decided, writes the
// audit entries (the decision, and block author and legal hold when set) and
// enqueues moderation.Decided. The row lock serialises concurrent decisions:
// the first commits, the others see a decided item and get
// model.ErrAlreadyDecided. A decision is final.
func (s *Service) Decide(ctx context.Context, p auth.Principal, itemID uuid.UUID, d model.Decision) error {
	role, err := staff(p)
	if err != nil {
		return err
	}
	if itemID == uuid.Nil {
		return model.ErrNotFound
	}
	now := s.now()
	var it model.Item
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		it, err = tx.GetItemForUpdate(ctx, itemID)
		if err != nil {
			return err
		}
		if it.Status != model.StatusOpen {
			return model.ErrAlreadyDecided
		}
		if err := model.ValidateDecision(it.Kind, d); err != nil {
			return err
		}
		updated, err := tx.DecideItem(ctx, it.ID, p.IdentityID, now, d)
		if err != nil {
			return err
		}
		if !updated {
			return model.ErrAlreadyDecided
		}
		for _, e := range auditEntries(p.IdentityID, role, it.ID, d) {
			if err := tx.Audit(ctx, e); err != nil {
				return err
			}
		}
		return tx.Publish(ctx, now, model.Event{Type: model.EventDecided, Version: 1, Data: model.DecidedData{
			ItemID: it.ID, Kind: it.Kind, ObjectID: it.ObjectID, Approve: d.Approve,
			Reason: d.Reason, BlockAuthor: d.BlockAuthor, LegalHold: d.LegalHold,
		}})
	}); err != nil {
		return fmt.Errorf("moderation: decide: %w", err)
	}
	s.log().InfoContext(ctx, "moderation item decided", "event", "ModerationItemDecided",
		"item_id", it.ID, "kind", it.Kind, "approve", d.Approve, "block_author", d.BlockAuthor, "legal_hold", d.LegalHold)
	return nil
}

func auditEntries(actor uuid.UUID, role string, itemID uuid.UUID, d model.Decision) []port.AuditEntry {
	base := port.AuditEntry{ActorID: actor, ActorRole: role, ObjectType: model.AuditObjectItem, ObjectID: itemID}
	decision := base
	if d.Approve {
		decision.Action = model.AuditActionApprove
	} else {
		decision.Action, decision.Reason = model.AuditActionReject, d.Reason
	}
	out := []port.AuditEntry{decision}
	if d.BlockAuthor {
		e := base
		e.Action = model.AuditActionBlock
		out = append(out, e)
	}
	if d.LegalHold {
		e := base
		e.Action = model.AuditActionLegalHold
		out = append(out, e)
	}
	return out
}
