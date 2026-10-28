package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
	"birge/internal/platform/auth"
)

// RaiseSos raises an SOS for the calling member. A repeated Idempotency-Key
// returns the SOS raised with it and does nothing else. At most
// model.MaxSosPerHour SOS per member per rolling hour (counted in the
// database under a per-member lock), otherwise model.ErrRateLimited.
//
// The SOS, emergency.SosRaised, the critical alert to the moderators, one
// alert per confirmed trusted contact (member contacts by member_id,
// external ones by trusted_contact_id: SMS without content) and the
// emergency.SosAutoClose timer commit in one transaction. The text goes into
// none of them; it is sealed with the author's key.
func (s *Service) RaiseSos(ctx context.Context, p auth.Principal, idempotencyKey uuid.UUID, sosType, text string) (model.SosView, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.SosView{}, err
	}
	if idempotencyKey == uuid.Nil || !model.SosTypeIsValid(sosType) {
		return model.SosView{}, model.ErrInvalidInput
	}
	if err := model.ValidateSosText(text); err != nil {
		return model.SosView{}, err
	}
	id := uuid.Must(uuid.NewV7())
	sealed, err := s.seal(ctx, memberID, text, model.FieldSosText, id)
	if err != nil {
		return model.SosView{}, err
	}
	now := s.now()
	var out model.Sos
	var replay bool
	var alerted int
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		if err := tx.Lock(ctx, model.LockSos, memberID); err != nil {
			return err
		}
		existing, err := tx.GetSosByIdempotencyKey(ctx, memberID, idempotencyKey)
		if err == nil {
			out, replay = existing, true
			return nil
		}
		if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		n, err := tx.CountSosSince(ctx, memberID, now.Add(-time.Hour))
		if err != nil {
			return err
		}
		if n >= model.MaxSosPerHour {
			return model.ErrRateLimited
		}
		created, inserted, err := tx.InsertSos(ctx, model.Sos{
			ID: id, MemberID: memberID, IdempotencyKey: idempotencyKey, Type: sosType, Text: sealed,
			Status: model.SosOpen, CreatedAt: now, ClosesAt: now.Add(s.autoCloseAfter()),
		})
		if err != nil {
			return err
		}
		if !inserted {
			// Unreachable under the lock; kept so a replay never double-alerts.
			existing, err := tx.GetSosByIdempotencyKey(ctx, memberID, idempotencyKey)
			if err != nil {
				return err
			}
			out, replay = existing, true
			return nil
		}
		out = created
		contacts, err := tx.ListActiveTrustedContacts(ctx, memberID)
		if err != nil {
			return err
		}
		alerted, err = s.publishRaised(ctx, tx, now, created, contacts)
		if err != nil {
			return err
		}
		_, err = tx.ScheduleAutoClose(ctx, created.ID, created.ClosesAt)
		return err
	})
	if err != nil {
		if errors.Is(err, model.ErrRateLimited) {
			s.log().Info("sos rate limited", "event", "SosRateLimited")
		}
		return model.SosView{}, err
	}
	if replay {
		s.log().Info("sos replayed", "event", "SosReplayed", "sos_id", out.ID)
	} else {
		// No member id in the log: an SOS is sensitive; the id is enough to trace.
		s.log().Info("sos raised", "event", model.EventSosRaised, "sos_id", out.ID, "type", out.Type, "contacts_alerted", alerted)
	}
	return s.view(ctx, out)
}

// publishRaised publishes SosRaised and the alerts of a new SOS; it returns
// how many trusted contacts were alerted.
func (s *Service) publishRaised(ctx context.Context, tx port.Tx, now time.Time, sos model.Sos, contacts []model.TrustedContact) (int, error) {
	if err := tx.Publish(ctx, now, model.Event{
		Type: model.EventSosRaised, Version: model.EventVersion,
		Data: model.SosRaisedData{SosID: sos.ID, MemberID: sos.MemberID, Type: sos.Type},
	}); err != nil {
		return 0, err
	}
	params := map[string]string{"sos_id": sos.ID.String()}
	if err := tx.Publish(ctx, now, model.Delivery(sos.ID, model.DeliveryRecipient{StaffRole: model.StaffModerator},
		model.ClassCritical, model.TemplateSosRaised, params)); err != nil {
		return 0, err
	}
	// N5: for childcare, too, only the confirmed trusted contacts are told;
	// invited (unconfirmed) contacts never are.
	n := 0
	for _, c := range contacts {
		if !c.Confirmed() {
			continue
		}
		if err := tx.Publish(ctx, now, model.Delivery(sos.ID, model.ContactRecipient(c),
			model.ClassCritical, model.TemplateSosRaisedContact, params)); err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}

// view opens the SOS text for its author.
func (s *Service) view(ctx context.Context, sos model.Sos) (model.SosView, error) {
	text, err := s.open(ctx, sos.MemberID, sos.Text, model.FieldSosText, sos.ID)
	if err != nil {
		return model.SosView{}, err
	}
	return model.SosView{Sos: sos, PlainText: text}, nil
}

// ListMySos returns the caller's latest SOS requests (at most
// model.MySosLimit), newest first, with their texts.
func (s *Service) ListMySos(ctx context.Context, p auth.Principal) ([]model.SosView, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return nil, err
	}
	var list []model.Sos
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		list, err = tx.ListMySos(ctx, memberID, model.MySosLimit)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]model.SosView, 0, len(list))
	for _, sos := range list {
		v, err := s.view(ctx, sos)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ownSos returns the caller's SOS, locked when forUpdate. Another member's
// SOS is model.ErrNotFound, so its existence is not revealed.
func ownSos(ctx context.Context, tx port.Tx, memberID, id uuid.UUID, forUpdate bool) (model.Sos, error) {
	sos, err := tx.GetSos(ctx, id, forUpdate)
	if err != nil {
		return model.Sos{}, err
	}
	if sos.MemberID != memberID {
		return model.Sos{}, model.ErrNotFound
	}
	return sos, nil
}

// CloseSos closes the caller's SOS: it cancels the auto-close timer and
// publishes emergency.SosClosed, in one transaction. Closing a closed SOS
// returns it unchanged.
func (s *Service) CloseSos(ctx context.Context, p auth.Principal, id uuid.UUID) (model.SosView, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.SosView{}, err
	}
	now := s.now()
	var out model.Sos
	var closed bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		sos, err := ownSos(ctx, tx, memberID, id, true)
		if err != nil {
			return err
		}
		out = sos
		if sos.Status != model.SosOpen {
			return nil
		}
		updated, ok, err := tx.CloseSos(ctx, id, model.CloseByAuthor, nil, now)
		if err != nil || !ok {
			return err
		}
		updated.ResponseCount = sos.ResponseCount
		out, closed = updated, true
		if _, err := tx.CancelAutoClose(ctx, id, now); err != nil {
			return err
		}
		return tx.Publish(ctx, now, model.Event{
			Type: model.EventSosClosed, Version: model.EventVersion, Data: model.SosClosedData{SosID: id},
		})
	})
	if err != nil {
		return model.SosView{}, err
	}
	if closed {
		s.log().Info("sos closed", "event", model.EventSosClosed, "sos_id", id, "reason", model.CloseByAuthor)
	}
	return s.view(ctx, out)
}

// HandleAutoClose runs the emergency.SosAutoClose timer inside the timer
// worker's transaction tx: an SOS still open is closed and
// emergency.SosClosed published; a closed or unknown one is left alone.
// Idempotent: the row is locked and its status checked.
func (s *Service) HandleAutoClose(ctx context.Context, tx port.Tx, sosID uuid.UUID) error {
	sos, err := tx.GetSos(ctx, sosID, true)
	if errors.Is(err, model.ErrNotFound) {
		s.log().Warn("auto-close timer for unknown sos", "event", "SosAutoCloseOrphan", "sos_id", sosID)
		return nil
	}
	if err != nil {
		return err
	}
	if sos.Status != model.SosOpen {
		return nil
	}
	now := s.now()
	if _, ok, err := tx.CloseSos(ctx, sosID, model.CloseAuto, nil, now); err != nil || !ok {
		return err
	}
	if err := tx.Publish(ctx, now, model.Event{
		Type: model.EventSosClosed, Version: model.EventVersion, Data: model.SosClosedData{SosID: sosID},
	}); err != nil {
		return err
	}
	s.log().Info("sos closed", "event", model.EventSosClosed, "sos_id", sosID, "reason", model.CloseAuto)
	return nil
}

// ShareSos puts the caller's open SOS into her district feed and publishes
// emergency.SosShared (notify will push the district later). Only verified
// members (model.ErrForbidden otherwise); a childcare SOS is never shareable
// (N5, model.ErrConflict), nor a closed one, nor one of a member without a
// district. Sharing a shared SOS returns it unchanged.
func (s *Service) ShareSos(ctx context.Context, p auth.Principal, id uuid.UUID) (model.SosView, error) {
	memberID, err := memberOf(p)
	if err != nil {
		return model.SosView{}, err
	}
	prof, err := s.profile(ctx, memberID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return model.SosView{}, err
	}
	now := s.now()
	var out model.Sos
	var shared bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		sos, err := ownSos(ctx, tx, memberID, id, true)
		if err != nil {
			return err
		}
		out = sos
		switch {
		case !model.Shareable(sos.Type):
			return fmt.Errorf("%w: childcare is never shared", model.ErrConflict)
		case sos.Shared():
			return nil
		case sos.Status != model.SosOpen:
			return fmt.Errorf("%w: closed", model.ErrConflict)
		case !prof.Verified:
			return model.ErrForbidden
		case prof.DistrictID == nil || *prof.DistrictID == uuid.Nil:
			return fmt.Errorf("%w: no district", model.ErrConflict)
		}
		updated, ok, err := tx.ShareSos(ctx, id, *prof.DistrictID, now)
		if err != nil {
			return err
		}
		if !ok {
			return model.ErrConflict
		}
		updated.ResponseCount = sos.ResponseCount
		out, shared = updated, true
		return tx.Publish(ctx, now, model.Event{
			Type: model.EventSosShared, Version: model.EventVersion,
			Data: model.SosSharedData{SosID: id, DistrictID: *prof.DistrictID},
		})
	})
	if err != nil {
		return model.SosView{}, err
	}
	if shared {
		s.log().Info("sos shared", "event", model.EventSosShared, "sos_id", id)
	}
	return s.view(ctx, out)
}
