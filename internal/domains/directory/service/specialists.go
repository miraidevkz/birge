package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"birge/internal/domains/directory/model"
	"birge/internal/domains/directory/port"
	"birge/internal/platform/auth"
)

// SuggestSpecialist creates a pending specialist (verified only) and emits
// directory.ItemSubmitted. Idempotent per Idempotency-Key.
func (s *Service) SuggestSpecialist(
	ctx context.Context,
	p auth.Principal,
	idempotencyKey uuid.UUID,
	in model.NewSpecialist,
) (model.SpecialistView, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.SpecialistView{}, err
	}
	if idempotencyKey == uuid.Nil {
		return model.SpecialistView{}, fmt.Errorf("%w: idempotency key", model.ErrInvalidInput)
	}
	if err := validateSpecialist(in); err != nil {
		return model.SpecialistView{}, err
	}
	if err := s.requireVerified(ctx, memberID); err != nil {
		return model.SpecialistView{}, err
	}

	now := s.now()
	id, err := uuid.NewV7()
	if err != nil {
		return model.SpecialistView{}, fmt.Errorf("directory: specialist id: %w", err)
	}
	needs := append([]string{}, in.Needs...)
	var contacts *string
	if in.Contacts != nil {
		c := strings.TrimSpace(*in.Contacts)
		if c != "" {
			contacts = &c
		}
	}
	row := model.Specialist{
		ID: id, AddedBy: memberID, IdempotencyKey: idempotencyKey,
		Name: strings.TrimSpace(in.Name), Profession: in.Profession, DistrictID: in.DistrictID,
		Needs: needs, Contacts: contacts, Status: model.StatusPending, CreatedAt: now,
	}

	var out model.Specialist
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		created, inserted, err := tx.InsertSpecialist(ctx, row)
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := tx.GetSpecialistByIdempotencyKey(ctx, memberID, idempotencyKey)
			if err != nil {
				return err
			}
			out = existing
			return nil
		}
		if err := tx.Publish(ctx, now, model.Event{
			Type: model.EventItemSubmitted, Version: model.EventVersion,
			Data: model.ItemSubmittedData{ObjectType: model.ObjectSpecialist, ObjectID: created.ID},
		}); err != nil {
			return err
		}
		out = created
		return nil
	})
	if err != nil {
		return model.SpecialistView{}, err
	}
	if out.ID == row.ID {
		s.log().Info("specialist submitted", "event", "SpecialistSubmitted", "specialist_id", out.ID)
	}
	return model.SpecialistView{Specialist: out, ReviewCount: 0, LatestReviews: []model.ReviewView{}}, nil
}

// SearchSpecialists returns active specialists matching optional filters,
// with average rating over active reviews and the three latest active reviews.
func (s *Service) SearchSpecialists(
	ctx context.Context,
	p auth.Principal,
	profession, need *string,
	districtID *uuid.UUID,
	cursor string,
) (model.SearchPage, error) {
	if _, err := memberOf(p); err != nil {
		return model.SearchPage{}, err
	}
	if profession != nil && !model.CodeIsValid(*profession) {
		return model.SearchPage{}, fmt.Errorf("%w: profession", model.ErrInvalidInput)
	}
	if need != nil && !model.CodeIsValid(*need) {
		return model.SearchPage{}, fmt.Errorf("%w: need", model.ErrInvalidInput)
	}
	f := model.SearchFilter{
		Profession: profession, DistrictID: districtID, Need: need, Limit: model.SearchPageLimit,
	}
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return model.SearchPage{}, err
		}
		f.CursorCreatedAt, f.CursorID = &at, &id
	}

	var specs []model.Specialist
	var packs []struct {
		stats   model.RatingStats
		reviews []model.Review
	}
	authorIDs := make([]uuid.UUID, 0)
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		specs, err = tx.SearchActiveSpecialists(ctx, f)
		if err != nil {
			return err
		}
		packs = make([]struct {
			stats   model.RatingStats
			reviews []model.Review
		}, len(specs))
		for i, sp := range specs {
			stats, err := tx.AverageActiveRating(ctx, sp.ID)
			if err != nil {
				return err
			}
			revs, err := tx.ListLatestActiveReviews(ctx, sp.ID, model.LatestReviewsLimit)
			if err != nil {
				return err
			}
			packs[i] = struct {
				stats   model.RatingStats
				reviews []model.Review
			}{stats: stats, reviews: revs}
			for _, r := range revs {
				authorIDs = append(authorIDs, r.AuthorID)
			}
		}
		return nil
	})
	if err != nil {
		return model.SearchPage{}, err
	}

	page := model.SearchPage{Items: []model.SpecialistView{}}
	if len(specs) == int(f.Limit) {
		last := specs[len(specs)-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	profs, err := s.profiles(ctx, authorIDs...)
	if err != nil {
		return model.SearchPage{}, err
	}
	for i, sp := range specs {
		v := model.SpecialistView{
			Specialist: sp, ReviewCount: packs[i].stats.Count, LatestReviews: []model.ReviewView{},
		}
		if sp.Needs == nil {
			v.Needs = []string{}
		}
		if packs[i].stats.Count > 0 {
			avg := float32(packs[i].stats.Average)
			v.AverageRating = &avg
		}
		for _, r := range packs[i].reviews {
			v.LatestReviews = append(v.LatestReviews, model.ReviewView{
				Review: r, AuthorName: firstName(profs[r.AuthorID].DisplayName),
			})
		}
		page.Items = append(page.Items, v)
	}
	return page, nil
}

// PutReview creates or replaces the caller's review of a specialist (one per
// member), returns it to pending and emits directory.ItemSubmitted.
func (s *Service) PutReview(
	ctx context.Context,
	p auth.Principal,
	specialistID uuid.UUID,
	in model.NewReview,
) (model.ReviewView, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.ReviewView{}, err
	}
	if err := validateReview(in); err != nil {
		return model.ReviewView{}, err
	}
	if err := s.requireVerified(ctx, memberID); err != nil {
		return model.ReviewView{}, err
	}

	now := s.now()
	id, err := uuid.NewV7()
	if err != nil {
		return model.ReviewView{}, fmt.Errorf("directory: review id: %w", err)
	}
	var out model.Review
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		sp, err := tx.GetSpecialist(ctx, specialistID, false)
		if err != nil {
			return err
		}
		if sp.Status != model.StatusActive && sp.Status != model.StatusPending {
			return model.ErrNotFound
		}
		created, err := tx.UpsertReview(ctx, model.Review{
			ID: id, SpecialistID: specialistID, AuthorID: memberID,
			Rating: in.Rating, Text: strings.TrimSpace(in.Text),
			Status: model.StatusPending, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if err := tx.Publish(ctx, now, model.Event{
			Type: model.EventItemSubmitted, Version: model.EventVersion,
			Data: model.ItemSubmittedData{ObjectType: model.ObjectReview, ObjectID: created.ID},
		}); err != nil {
			return err
		}
		out = created
		return nil
	})
	if err != nil {
		return model.ReviewView{}, err
	}
	profs, err := s.profiles(ctx, memberID)
	if err != nil {
		return model.ReviewView{}, err
	}
	s.log().Info("review submitted", "event", "ReviewSubmitted", "review_id", out.ID)
	return model.ReviewView{Review: out, AuthorName: firstName(profs[memberID].DisplayName)}, nil
}
