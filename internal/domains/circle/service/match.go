package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/port"
	"birge/internal/platform/auth"
)

// ListMyMatches returns pair cards for the caller (no contacts).
func (s *Service) ListMyMatches(ctx context.Context, p auth.Principal) ([]model.MatchCard, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return nil, err
	}
	var matches []model.Match
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		list, err := tx.ListMatchesForMember(ctx, memberID, model.MaxMatchesListed)
		if err != nil {
			return err
		}
		matches = list
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.cardsFor(ctx, memberID, matches)
}

// AnswerMatch records "Let's" / "Not now". Two accepts → accepted +
// circle.MatchAccepted + AskMet timer; one decline → declined.
func (s *Service) AnswerMatch(ctx context.Context, p auth.Principal, matchID uuid.UUID, accept bool) (model.MatchCard, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.MatchCard{}, err
	}
	if matchID == uuid.Nil {
		return model.MatchCard{}, fmt.Errorf("%w: match id", model.ErrInvalidInput)
	}
	now := s.now()
	var updated model.Match
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		m, err := tx.GetMatch(ctx, matchID, true)
		if err != nil {
			return err
		}
		if _, ok := m.OtherMember(memberID); !ok {
			return model.ErrNotFound
		}
		if m.Status != model.MatchProposed {
			updated = m
			return nil
		}
		answerA, answerB := m.AnswerA, m.AnswerB
		switch memberID {
		case m.MemberA:
			if answerA != nil {
				updated = m
				return nil
			}
			answerA = ptrBool(accept)
		case m.MemberB:
			if answerB != nil {
				updated = m
				return nil
			}
			answerB = ptrBool(accept)
		}
		status := model.MatchProposed
		var askMetAt *time.Time
		aVal, aSet := boolVal(answerA)
		bVal, bSet := boolVal(answerB)
		if aSet && bSet {
			if aVal && bVal {
				status = model.MatchAccepted
				due := now.Add(s.askMetAfter())
				askMetAt = &due
			} else {
				status = model.MatchDeclined
			}
		}
		m, err = tx.UpdateMatchAnswers(ctx, m.ID, answerA, answerB, status, askMetAt)
		if err != nil {
			return err
		}
		switch status {
		case model.MatchAccepted:
			if err := tx.Publish(ctx, now, model.Event{
				Type:    model.EventMatchAccepted,
				Version: model.EventVersion,
				Data: model.MatchAcceptedData{
					MatchID: m.ID, MemberA: m.MemberA, MemberB: m.MemberB,
				},
			}); err != nil {
				return err
			}
			if askMetAt != nil {
				if _, err := tx.ScheduleAskMet(ctx, m.ID, *askMetAt); err != nil {
					return err
				}
			}
			s.log().Info("match accepted", "event", "MatchAccepted", "match_id", m.ID)
		case model.MatchDeclined:
			s.log().Info("match declined", "event", "MatchDeclined", "match_id", m.ID)
		}
		updated = m
		return nil
	})
	if err != nil {
		return model.MatchCard{}, err
	}
	cards, err := s.cardsFor(ctx, memberID, []model.Match{updated})
	if err != nil {
		return model.MatchCard{}, err
	}
	if len(cards) == 0 {
		return model.MatchCard{}, model.ErrNotFound
	}
	return cards[0], nil
}

// ConfirmMet records the "did you meet?" answer. Two yes → acquaintance +
// circle.AcquaintanceEstablished; one no → not_met.
func (s *Service) ConfirmMet(ctx context.Context, p auth.Principal, matchID uuid.UUID, met bool) (model.MatchCard, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.MatchCard{}, err
	}
	if matchID == uuid.Nil {
		return model.MatchCard{}, fmt.Errorf("%w: match id", model.ErrInvalidInput)
	}
	now := s.now()
	var updated model.Match
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		m, err := tx.GetMatch(ctx, matchID, true)
		if err != nil {
			return err
		}
		if _, ok := m.OtherMember(memberID); !ok {
			return model.ErrNotFound
		}
		if m.Status != model.MatchAccepted && m.Status != model.MatchMet && m.Status != model.MatchNotMet {
			return model.ErrConflict
		}
		if m.Status == model.MatchMet || m.Status == model.MatchNotMet {
			updated = m
			return nil
		}
		metA, metB := m.MetA, m.MetB
		switch memberID {
		case m.MemberA:
			if metA != nil {
				updated = m
				return nil
			}
			metA = ptrBool(met)
		case m.MemberB:
			if metB != nil {
				updated = m
				return nil
			}
			metB = ptrBool(met)
		}
		status := model.MatchAccepted
		aVal, aSet := boolVal(metA)
		bVal, bSet := boolVal(metB)
		if aSet && bSet {
			if aVal && bVal {
				status = model.MatchMet
			} else {
				status = model.MatchNotMet
			}
		}
		m, err = tx.UpdateMatchMet(ctx, m.ID, metA, metB, status)
		if err != nil {
			return err
		}
		if status == model.MatchMet {
			if _, _, err := tx.InsertAcquaintance(ctx, m.MemberA, m.MemberB, now); err != nil {
				return err
			}
			if err := tx.Publish(ctx, now, model.Event{
				Type:    model.EventAcquaintanceEstablished,
				Version: model.EventVersion,
				Data:    model.AcquaintanceEstablishedData{MemberA: m.MemberA, MemberB: m.MemberB},
			}); err != nil {
				return err
			}
			s.log().Info("acquaintance established", "event", "AcquaintanceEstablished", "match_id", m.ID)
		}
		updated = m
		return nil
	})
	if err != nil {
		return model.MatchCard{}, err
	}
	cards, err := s.cardsFor(ctx, memberID, []model.Match{updated})
	if err != nil {
		return model.MatchCard{}, err
	}
	if len(cards) == 0 {
		return model.MatchCard{}, model.ErrNotFound
	}
	return cards[0], nil
}

// HandleAskMet notifies both members to confirm they met (template circle.ask_met).
func (s *Service) HandleAskMet(ctx context.Context, tx port.Tx, matchID uuid.UUID) error {
	m, err := tx.GetMatch(ctx, matchID, true)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.Status != model.MatchAccepted {
		return nil
	}
	now := s.now()
	if m.AskMetAt == nil {
		if _, _, err := tx.SetMatchAskMetAt(ctx, matchID, now); err != nil {
			return err
		}
	}
	params := map[string]string{"match_id": matchID.String()}
	for _, memberID := range []uuid.UUID{m.MemberA, m.MemberB} {
		if err := tx.Publish(ctx, now, model.Delivery(matchID, memberID, model.TemplateAskMet, params)); err != nil {
			return err
		}
	}
	s.log().Info("ask met notified", "event", "AskMetNotified", "match_id", matchID)
	return nil
}

func (s *Service) cardsFor(ctx context.Context, viewer uuid.UUID, matches []model.Match) ([]model.MatchCard, error) {
	if len(matches) == 0 {
		return []model.MatchCard{}, nil
	}
	ids := make([]uuid.UUID, 0, len(matches)+1)
	ids = append(ids, viewer)
	for _, m := range matches {
		if other, ok := m.OtherMember(viewer); ok {
			ids = append(ids, other)
		}
	}
	profs, err := s.profiles(ctx, ids...)
	if err != nil {
		return nil, err
	}
	viewerProf := profs[viewer]
	now := s.now()
	out := make([]model.MatchCard, 0, len(matches))
	for _, m := range matches {
		other, ok := m.OtherMember(viewer)
		if !ok {
			continue
		}
		op, ok := profs[other]
		if !ok {
			continue
		}
		out = append(out, model.MatchCard{
			Match:            m,
			OtherDisplayName: op.FirstName,
			OtherDistrictID:  op.DistrictID,
			OtherChildAges:   model.ChildAges(op, now),
			SharedNeeds:      model.SharedNeeds(viewerProf, op),
			MyAnswer:         m.MyAnswer(viewer),
		})
	}
	return out, nil
}
