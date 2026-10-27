package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/port"
)

// HandleMatchRun pairs open tea requests and re-schedules itself for the next
// interval. Must run on the timer worker's transaction so CancelMatchRun can
// clear the active dedup row before ScheduleMatchRun (ADR-0020).
func (s *Service) HandleMatchRun(ctx context.Context, tx port.Tx) error {
	now := s.now()
	if err := s.runMatching(ctx, tx, now); err != nil {
		return err
	}
	if _, err := tx.CancelMatchRun(ctx, now); err != nil {
		return err
	}
	if _, err := tx.ScheduleMatchRun(ctx, now.Add(s.matchInterval())); err != nil {
		return err
	}
	s.log().Info("match run completed", "event", "MatchRunCompleted")
	return nil
}

func (s *Service) runMatching(ctx context.Context, tx port.Tx, now time.Time) error {
	reqs, err := tx.ListOpenTeaRequests(ctx, now)
	if err != nil {
		return err
	}
	if len(reqs) < 2 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(reqs))
	for _, r := range reqs {
		ids = append(ids, r.MemberID)
	}
	profs, err := s.profiles(ctx, ids...)
	if err != nil {
		return err
	}
	cands := make([]model.Candidate, 0, len(reqs))
	for _, r := range reqs {
		p, ok := profs[r.MemberID]
		if !ok || !p.Verified {
			continue
		}
		cands = append(cands, model.Candidate{Request: r, Profile: p})
	}
	since := now.Add(-s.rematchCooldown())
	pairs, err := model.PairOpenRequests(
		cands,
		s.weights(),
		func(a, b uuid.UUID) (bool, error) { return s.blocked(ctx, a, b) },
		func(a, b uuid.UUID) (bool, error) { return tx.PairMatchedSince(ctx, a, b, since) },
	)
	if err != nil {
		return err
	}
	for _, pair := range pairs {
		if err := s.proposePair(ctx, tx, now, pair); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) proposePair(ctx context.Context, tx port.Tx, now time.Time, pair model.PairScore) error {
	ra, rb := pair.A.Request, pair.B.Request
	memberA, memberB := model.CanonicalPair(ra.MemberID, rb.MemberID)
	reqA, reqB := ra, rb
	if ra.MemberID != memberA {
		reqA, reqB = rb, ra
	}
	if _, updated, err := tx.MarkTeaRequestMatched(ctx, reqA.ID); err != nil {
		return err
	} else if !updated {
		return nil
	}
	if _, updated, err := tx.MarkTeaRequestMatched(ctx, reqB.ID); err != nil {
		return err
	} else if !updated {
		return fmt.Errorf("circle: request %s not open after pairing", reqB.ID)
	}
	if _, err := tx.CancelRequestExpire(ctx, reqA.ID, now); err != nil {
		return err
	}
	if _, err := tx.CancelRequestExpire(ctx, reqB.ID, now); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("circle: match id: %w", err)
	}
	m, err := tx.InsertMatch(ctx, model.Match{
		ID:         id,
		MemberA:    memberA,
		MemberB:    memberB,
		RequestAID: reqA.ID,
		RequestBID: reqB.ID,
		Format:     reqA.Format,
		Status:     model.MatchProposed,
		CreatedAt:  now,
	})
	if err != nil {
		return err
	}
	params := map[string]string{"match_id": m.ID.String()}
	for _, memberID := range []uuid.UUID{memberA, memberB} {
		if err := tx.Publish(ctx, now, model.Delivery(m.ID, memberID, model.TemplateMatchProposed, params)); err != nil {
			return err
		}
	}
	s.log().Info("match proposed", "event", "MatchProposed", "match_id", m.ID)
	return nil
}
