package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/port"
	"birge/internal/platform/auth"
)

// Met reports whether a and b both confirmed they met in person (§ 3: a
// property of the pair). Symmetric. Used by emergency as Acquaintances.Met.
func (s *Service) Met(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if a == uuid.Nil || b == uuid.Nil || a == b {
		return false, nil
	}
	lo, hi := model.CanonicalPair(a, b)
	var found bool
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		_, err := tx.GetAcquaintance(ctx, lo, hi)
		if err == nil {
			found = true
			return nil
		}
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		return err
	})
	return found, err
}

// ListAcquaintances returns the caller's confirmed acquaintances with first names.
func (s *Service) ListAcquaintances(ctx context.Context, p auth.Principal) ([]model.AcquaintanceView, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return nil, err
	}
	var rows []model.Acquaintance
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		list, err := tx.ListAcquaintancesForMember(ctx, memberID)
		if err != nil {
			return err
		}
		rows = list
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []model.AcquaintanceView{}, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if other, ok := r.Other(memberID); ok {
			ids = append(ids, other)
		}
	}
	profs, err := s.profiles(ctx, ids...)
	if err != nil {
		return nil, err
	}
	out := make([]model.AcquaintanceView, 0, len(rows))
	for _, r := range rows {
		other, ok := r.Other(memberID)
		if !ok {
			continue
		}
		name := ""
		if p, ok := profs[other]; ok {
			name = p.FirstName
		}
		out = append(out, model.AcquaintanceView{
			MemberID:    other,
			DisplayName: name,
			Since:       r.Since,
		})
		if len(out) >= model.MaxAcquaintancesListed {
			break
		}
	}
	return out, nil
}

// SetRoomID stores the private chat room id for a match after chat creates it.
// TODO(circle): wire from chat's MatchAccepted consumer once chat exists.
func (s *Service) SetRoomID(ctx context.Context, matchID, roomID uuid.UUID) error {
	if matchID == uuid.Nil || roomID == uuid.Nil {
		return fmt.Errorf("%w: match or room id", model.ErrInvalidInput)
	}
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		_, updated, err := tx.SetMatchRoomID(ctx, matchID, roomID)
		if err != nil {
			return err
		}
		if !updated {
			// Already set or unknown: treat unknown as not found.
			if _, err := tx.GetMatch(ctx, matchID, false); err != nil {
				return err
			}
		}
		return nil
	})
}
