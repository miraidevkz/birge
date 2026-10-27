package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"birge/internal/domains/directory/model"
	"birge/internal/domains/directory/port"
)

// SpecialistSummary returns a specialist card without contacts for the
// moderation ContentSource. Gone → model.ErrNotFound.
func (s *Service) SpecialistSummary(ctx context.Context, specialistID uuid.UUID) (string, error) {
	if specialistID == uuid.Nil {
		return "", model.ErrNotFound
	}
	var sp model.Specialist
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		sp, err = tx.GetSpecialist(ctx, specialistID, false)
		return err
	})
	if errors.Is(err, model.ErrNotFound) {
		return "", model.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	parts := []string{strings.TrimSpace(sp.Name), strings.TrimSpace(sp.Profession)}
	if sp.DistrictID != uuid.Nil {
		parts = append(parts, sp.DistrictID.String())
	}
	if len(sp.Needs) > 0 {
		parts = append(parts, strings.Join(sp.Needs, ", "))
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · "), nil
}

// ReviewSummary returns review text (and rating) for the moderation
// ContentSource. Gone → model.ErrNotFound. No author contacts.
func (s *Service) ReviewSummary(ctx context.Context, reviewID uuid.UUID) (string, error) {
	if reviewID == uuid.Nil {
		return "", model.ErrNotFound
	}
	var r model.Review
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		r, err = tx.GetReview(ctx, reviewID, false)
		return err
	})
	if errors.Is(err, model.ErrNotFound) {
		return "", model.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(r.Text)
	if text == "" {
		return "", model.ErrNotFound
	}
	return text, nil
}
