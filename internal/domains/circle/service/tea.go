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

// CreateTeaRequest creates an open tea request for a verified member.
// One open request per member (409). Idempotent per Idempotency-Key.
// A new request publishes circle.TeaRequested in the same transaction.
func (s *Service) CreateTeaRequest(
	ctx context.Context,
	p auth.Principal,
	idempotencyKey uuid.UUID,
	format string,
	prefs model.Preferences,
) (model.TeaRequest, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.TeaRequest{}, err
	}
	if idempotencyKey == uuid.Nil {
		return model.TeaRequest{}, fmt.Errorf("%w: idempotency key", model.ErrInvalidInput)
	}
	if !model.FormatIsValid(format) {
		return model.TeaRequest{}, fmt.Errorf("%w: format", model.ErrInvalidInput)
	}

	prof, err := s.profile(ctx, memberID)
	if err != nil {
		return model.TeaRequest{}, err
	}
	if !prof.Verified {
		return model.TeaRequest{}, model.ErrForbidden
	}

	now := s.now()
	id, err := uuid.NewV7()
	if err != nil {
		return model.TeaRequest{}, fmt.Errorf("circle: request id: %w", err)
	}
	req := model.TeaRequest{
		ID:             id,
		MemberID:       memberID,
		IdempotencyKey: idempotencyKey,
		Format:         format,
		Preferences:    prefs,
		Status:         model.RequestOpen,
		CreatedAt:      now,
		ExpiresAt:      now.Add(s.requestTTL()),
	}

	var out model.TeaRequest
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		created, inserted, err := tx.InsertTeaRequest(ctx, req)
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := tx.GetTeaRequestByIdempotencyKey(ctx, memberID, idempotencyKey)
			if err != nil {
				return err
			}
			out = existing
			return nil
		}
		// Only a new row publishes: a repeated Idempotency-Key returned above
		// and a second open request rolled back with ErrConflict, so the funnel
		// counts each request exactly once.
		if err := tx.Publish(ctx, now, model.Event{
			Type:    model.EventTeaRequested,
			Version: model.EventVersion,
			Data: model.TeaRequestedData{
				RequestID: created.ID, MemberID: created.MemberID, Format: created.Format,
			},
		}); err != nil {
			return err
		}
		if _, err := tx.ScheduleRequestExpire(ctx, created.ID, created.ExpiresAt); err != nil {
			return err
		}
		// Keep the hourly matcher alive; no-op when an active MatchRun exists.
		if _, err := tx.ScheduleMatchRun(ctx, now.Add(s.matchInterval())); err != nil {
			return err
		}
		out = created
		return nil
	})
	if err != nil {
		return model.TeaRequest{}, err
	}
	if out.ID == req.ID {
		s.log().Info("tea request created", "event", "TeaRequestCreated", "request_id", out.ID)
	}
	return out, nil
}

// ListMyTeaRequests returns the caller's latest tea requests.
func (s *Service) ListMyTeaRequests(ctx context.Context, p auth.Principal) ([]model.TeaRequest, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return nil, err
	}
	var out []model.TeaRequest
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		list, err := tx.ListTeaRequestsByMember(ctx, memberID, model.MaxTeaRequestsListed)
		if err != nil {
			return err
		}
		out = list
		return nil
	})
	return out, err
}

// CancelTeaRequest cancels the caller's open request; idempotent.
func (s *Service) CancelTeaRequest(ctx context.Context, p auth.Principal, requestID uuid.UUID) error {
	memberID, err := memberOf(p)
	if err != nil {
		return err
	}
	if requestID == uuid.Nil {
		return fmt.Errorf("%w: request id", model.ErrInvalidInput)
	}
	now := s.now()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		existing, err := tx.GetTeaRequest(ctx, requestID, false)
		if errors.Is(err, model.ErrNotFound) {
			return model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if existing.MemberID != memberID {
			return model.ErrNotFound
		}
		if existing.Status != model.RequestOpen {
			return nil
		}
		if _, updated, err := tx.CancelTeaRequest(ctx, requestID, memberID); err != nil {
			return err
		} else if !updated {
			return nil
		}
		if _, err := tx.CancelRequestExpire(ctx, requestID, now); err != nil {
			return err
		}
		s.log().Info("tea request cancelled", "event", "TeaRequestCancelled", "request_id", requestID)
		return nil
	})
}

// HandleRequestExpire expires an open tea request when its timer fires.
func (s *Service) HandleRequestExpire(ctx context.Context, tx port.Tx, requestID uuid.UUID) error {
	_, updated, err := tx.ExpireTeaRequest(ctx, requestID)
	if err != nil {
		return err
	}
	if updated {
		s.log().Info("tea request expired", "event", "TeaRequestExpired", "request_id", requestID)
	}
	return nil
}
