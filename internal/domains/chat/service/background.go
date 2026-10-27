package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
)

// HandleCrisisEscalation runs the chat.CrisisEscalation timer inside the
// worker's transaction: it opens the escalation of a crisis message that has
// none yet. OpenEscalation is idempotent per member while one is open, so a
// retry never opens a second one; an error makes the worker retry with
// backoff, so the escalation cannot be lost.
func (s *Service) HandleCrisisEscalation(ctx context.Context, tx port.Tx, messageID uuid.UUID) error {
	m, err := tx.GetMessage(ctx, messageID, true)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.EscalationID != nil || m.HoldReason != model.HoldCrisis {
		return nil
	}
	if s.Crisis == nil {
		return errors.New("chat: crisis not configured")
	}
	escID, err := s.Crisis.OpenEscalation(ctx, m.AuthorID, model.EscalationSource)
	if err != nil {
		s.log().Error("chat crisis escalation retry failed", "event", "ChatCrisisEscalationDeferred", "message_id", m.ID, "error", err)
		return err
	}
	s.log().Info("chat crisis escalation opened", "event", "ChatCrisisEscalationOpened", "message_id", m.ID, "escalation_id", escID)
	return tx.SetMessageEscalation(ctx, m.ID, escID)
}

// HandlePush runs the chat.PushFanout timer inside the worker's transaction:
// one content-free notify.DeliveryRequested (class normal, template
// chat.new_message, params {room_id}) per room member who is not the author,
// has not muted the room, is not connected to it, was not pushed for it in
// the last PushInterval, and is not blocked with the author either way.
// Request ids are deterministic, so a retried run cannot push twice.
func (s *Service) HandlePush(ctx context.Context, tx port.Tx, messageID uuid.UUID) error {
	m, err := tx.GetMessage(ctx, messageID, false)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.Status != model.StatusVisible {
		return nil
	}
	now := s.now()
	candidates, err := tx.ListPushCandidates(ctx, m.RoomID, m.AuthorID, now, now.Add(-s.pushInterval()))
	if err != nil {
		return err
	}
	sent := 0
	for _, member := range candidates {
		b, err := s.blocked(ctx, m.AuthorID, member)
		if err != nil {
			return err
		}
		if b {
			continue
		}
		if err := tx.Publish(ctx, now, model.NewMessagePush(m.ID, m.RoomID, member)); err != nil {
			return err
		}
		if err := tx.MarkPushed(ctx, m.RoomID, member, now); err != nil {
			return err
		}
		sent++
	}
	if sent > 0 {
		s.log().Info("chat push requested", "event", "ChatPushRequested", "message_id", m.ID, "recipients", sent)
	}
	return nil
}

// EnsureRetentionTimer schedules the next daily retention sweep. Safe to
// call on every worker start and from several replicas: the timer's dedup
// key is its date.
func (s *Service) EnsureRetentionTimer(ctx context.Context) error {
	return s.Store.InTx(ctx, func(tx port.Tx) error { return s.scheduleRetention(ctx, tx) })
}

func (s *Service) scheduleRetention(ctx context.Context, tx port.Tx) error {
	next := model.NextRetentionRun(s.now(), s.retentionAt())
	_, err := tx.Schedule(ctx, model.TimerRetention, model.RetentionSubject, "retention|"+next.Format("2006-01-02"), next)
	return err
}

// HandleRetention runs the daily chat.RetentionPurge timer inside the
// worker's transaction: it clears the texts of group-room messages older
// than RetentionGroup and direct-room messages older than RetentionDirect,
// except messages under legal hold or an open report, and schedules the
// next day's run. A purged message stays as a "deleted" tombstone, so
// sequences and resume keep working.
func (s *Service) HandleRetention(ctx context.Context, tx port.Tx) error {
	now := s.now()
	var total int64
	for _, pass := range []struct {
		kinds []string
		keep  time.Duration
	}{
		{[]string{model.KindDistrict, model.KindTopic}, s.retentionGroup()},
		{[]string{model.KindDirect}, s.retentionDirect()},
	} {
		before := now.Add(-pass.keep)
		for {
			n, err := tx.PurgeExpired(ctx, pass.kinds, before, now, purgePage)
			if err != nil {
				return err
			}
			total += n
			if n < purgePage {
				break
			}
		}
	}
	s.log().Info("chat retention sweep", "event", "ChatRetentionPurge", "purged", total)
	return s.scheduleRetention(ctx, tx)
}
