package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/platform/auth"
)

// Send stores a message from the caller in a room she is in.
//
// Order (do not reorder, crisis-path skill): validation, the level-1 crisis
// check (local, before any network call; the member's language is not
// waited for),
// then the filters and limits. A crisis message is stored held (never
// visible to others), bypasses rate limit, slow mode and room mutes, and
// the author gets the crisis notice with the configured crisis phones. The
// escalation is opened synchronously after commit; a timer created in the
// insert transaction retries until it is open, so a crash or a safety
// outage cannot lose it.
//
// Otherwise a link or a phone number, or a newcomer's first group-room
// messages, hold the message and publish chat.MessageHeld; a visible
// message schedules the push fan-out. The sequence number is taken from the
// room row in the same transaction (see doc.go). A repeated clientID returns
// the stored message and changes nothing.
//
// When the crisis check fired but the message could not be stored (not a
// member, database down), the result still carries the crisis notice next
// to the error, and the escalation is attempted directly.
func (s *Service) Send(ctx context.Context, p auth.Principal, roomID, clientID uuid.UUID, text string) (model.SendResult, error) {
	author, err := memberOf(p)
	if err != nil {
		return model.SendResult{}, err
	}
	if err := model.ValidateClientID(clientID); err != nil {
		return model.SendResult{}, fmt.Errorf("%w: client id", err)
	}
	if err := model.ValidateText(text); err != nil {
		return model.SendResult{}, fmt.Errorf("%w: text", err)
	}
	if s.Crisis == nil {
		return model.SendResult{}, errors.New("chat: crisis detector not configured")
	}
	// Level 1 first, before any network call (crisis-path skill). Every
	// loaded language is checked; the language only orders them, so none is
	// passed rather than waiting for membership.
	isCrisis := s.Crisis.Detect(text, "")
	profile := s.profileBestEffort(ctx, author)
	var notice *model.CrisisNotice
	hold := ""
	if isCrisis {
		notice = &model.CrisisNotice{Phones: s.Crisis.CrisisPhones()}
		hold = model.HoldCrisis
	} else {
		hold = model.HoldFor(text)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return model.SendResult{}, fmt.Errorf("chat: message id: %w", err)
	}
	now := s.now()
	sealed, err := s.seal(ctx, author, text, id)
	if err != nil {
		return s.crisisFallback(ctx, author, notice, err)
	}

	var stored model.Message
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		if err := tx.Lock(ctx, model.LockSend, author); err != nil {
			return err
		}
		existing, err := tx.GetMessageByClientID(ctx, author, clientID)
		if err == nil {
			stored = existing
			return errReplay
		}
		if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		room, err := activeMember(ctx, tx, roomID, author)
		if err != nil {
			return err
		}
		msgHold := hold
		if !isCrisis {
			if err := s.checkLimits(ctx, tx, room, author); err != nil {
				return err
			}
			if msgHold == "" && room.Group() && s.newcomerHeld() > 0 {
				n, err := tx.CountAuthorGroupMessages(ctx, author, s.newcomerHeld())
				if err != nil {
					return err
				}
				if n < s.newcomerHeld() {
					msgHold = model.HoldNewcomer
				}
			}
		}
		seq, err := tx.NextSeq(ctx, roomID)
		if err != nil {
			return err
		}
		status := model.StatusVisible
		if msgHold != "" {
			status = model.StatusHeld
		}
		created, inserted, err := tx.InsertMessage(ctx, model.Message{
			ID: id, RoomID: roomID, Seq: seq, AuthorID: author, ClientID: clientID, Text: sealed,
			Status: status, HoldReason: msgHold, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !inserted {
			// Same client id committed concurrently (in another room): roll
			// back so the sequence number is given back.
			return errDuplicate
		}
		stored = created
		switch {
		case isCrisis:
			_, err = tx.Schedule(ctx, model.TimerCrisisEscalation, id, id.String(), now.Add(crisisRetryAfter))
		case msgHold != "":
			err = tx.Publish(ctx, now, model.MessageHeld(created))
		default:
			_, err = tx.Schedule(ctx, model.TimerPush, id, id.String(), now)
		}
		return err
	})
	switch {
	case errors.Is(err, errReplay), errors.Is(err, errDuplicate):
		return s.replay(ctx, author, clientID)
	case err != nil:
		return s.crisisFallback(ctx, author, notice, err)
	}

	if !isCrisis {
		// A crisis message is visible to its author only: no live notice, so
		// the crisis frame is the first thing her connection gets about it.
		// Her other devices see it held on their next catch-up.
		s.notify(ctx, model.Notice{Kind: model.NoticeNew, RoomID: roomID, MessageID: &stored.ID, Seq: stored.Seq})
	}
	if isCrisis {
		s.escalateNow(ctx, stored, notice)
	} else if stored.Status == model.StatusHeld {
		s.log().Info("chat message held", "event", model.EventMessageHeld, "message_id", stored.ID, "reason", stored.HoldReason)
	}
	view, _ := model.View(model.Opened{Message: stored, PlainText: text, AuthorName: profile.FirstName}, author)
	return model.SendResult{Message: view, Crisis: notice}, nil
}

// checkLimits applies room restrictions, the per-member rate limit and slow
// mode. It runs under the author's lock, so concurrent sends of one member
// cannot both pass.
func (s *Service) checkLimits(ctx context.Context, tx port.Tx, room model.Room, author uuid.UUID) error {
	now := s.now()
	r, err := tx.GetRestriction(ctx, room.ID, author)
	if err != nil {
		return err
	}
	if r.Blocks(now) {
		return model.ErrBanned
	}
	n, err := tx.CountAuthorMessagesSince(ctx, author, now.Add(-s.rateWindow()))
	if err != nil {
		return err
	}
	if n >= s.rateLimit() {
		return model.ErrRateLimited
	}
	if room.SlowModeSeconds > 0 {
		last, err := tx.LastAuthorMessageAt(ctx, room.ID, author)
		if err != nil {
			return err
		}
		if last != nil && now.Sub(*last) < secondsDur(room.SlowModeSeconds) {
			return model.ErrSlowMode
		}
	}
	return nil
}

// crisisFallback returns err, and when the crisis check fired, also the
// crisis notice after trying to open the escalation directly (the message
// could not be stored, so no retry timer exists for it).
func (s *Service) crisisFallback(ctx context.Context, author uuid.UUID, notice *model.CrisisNotice, err error) (model.SendResult, error) {
	if notice == nil {
		return model.SendResult{}, err
	}
	escID, escErr := s.Crisis.OpenEscalation(ctx, author, model.EscalationSource)
	if escErr != nil {
		s.log().Error("chat crisis escalation failed without a stored message", "event", "ChatCrisisEscalationFailed", "error", escErr)
	} else {
		notice.EscalationID = &escID
		s.log().Info("chat crisis escalation opened", "event", "ChatCrisisEscalationOpened", "escalation_id", escID)
	}
	return model.SendResult{Crisis: notice}, err
}

// escalateNow opens the escalation of a stored crisis message right after
// commit and records it; on failure the chat.CrisisEscalation timer retries.
func (s *Service) escalateNow(ctx context.Context, m model.Message, notice *model.CrisisNotice) {
	escID, err := s.Crisis.OpenEscalation(ctx, m.AuthorID, model.EscalationSource)
	if err != nil {
		s.log().Error("chat crisis escalation deferred to timer", "event", "ChatCrisisEscalationDeferred", "message_id", m.ID, "error", err)
		return
	}
	notice.EscalationID = &escID
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		if err := tx.SetMessageEscalation(ctx, m.ID, escID); err != nil {
			return err
		}
		return tx.CancelTimers(ctx, model.TimerCrisisEscalation, m.ID, s.now())
	})
	if err != nil {
		// The escalation is open; the timer will find it open (idempotent per member).
		s.log().Warn("chat crisis escalation not recorded", "event", "ChatCrisisEscalationUnrecorded", "message_id", m.ID, "error", err)
	}
	s.log().Info("chat crisis escalation opened", "event", "ChatCrisisEscalationOpened", "message_id", m.ID, "escalation_id", escID)
}

// replay returns the author's stored message for clientID.
func (s *Service) replay(ctx context.Context, author, clientID uuid.UUID) (model.SendResult, error) {
	var m model.Message
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		m, err = tx.GetMessageByClientID(ctx, author, clientID)
		return err
	})
	if err != nil {
		return model.SendResult{}, err
	}
	text, deleted, err := s.open(ctx, m)
	if err != nil {
		return model.SendResult{}, err
	}
	name := s.profileBestEffort(ctx, author).FirstName
	view, _ := model.View(model.Opened{Message: m, PlainText: text, Deleted: deleted, AuthorName: name}, author)
	res := model.SendResult{Message: view, Replayed: true}
	if m.HoldReason == model.HoldCrisis && s.Crisis != nil {
		res.Crisis = &model.CrisisNotice{Phones: s.Crisis.CrisisPhones(), EscalationID: m.EscalationID}
	}
	return res, nil
}

// ListMessages returns the room's history for the caller, ordered by seq:
// after a sequence (resume; default 0) or before one (scroll back). Held
// messages of others and messages of members blocked either way are left
// out; the page is filled from further rows, so a short page means the end.
func (s *Service) ListMessages(ctx context.Context, p auth.Principal, roomID uuid.UUID, after, before *int64, limit int) ([]model.MessageView, error) {
	reader, err := memberOf(p)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = model.PageDefault
	}
	if limit > model.PageMax || (after != nil && *after < 0) || (before != nil && *before < 1) || (after != nil && before != nil) {
		return nil, model.ErrInvalidInput
	}
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		_, err := activeMember(ctx, tx, roomID, reader)
		return err
	}); err != nil {
		return nil, err
	}
	r := s.newRenderer(reader)
	out := make([]model.MessageView, 0, limit)
	if before != nil {
		cursor := *before
		for len(out) < limit {
			raw, err := s.page(ctx, func(tx port.Tx) ([]model.Message, error) {
				return tx.ListMessagesBefore(ctx, roomID, cursor, renderBatch)
			})
			if err != nil {
				return nil, err
			}
			for _, m := range raw {
				cursor = m.Seq
				v, ok, err := r.render(ctx, s, m)
				if err != nil {
					return nil, err
				}
				if ok && len(out) < limit {
					out = append(out, v)
				}
			}
			if len(raw) < renderBatch {
				break
			}
		}
		slices.Reverse(out)
		return out, nil
	}
	cursor := int64(0)
	if after != nil {
		cursor = *after
	}
	for len(out) < limit {
		views, scanned, more, err := s.catchup(ctx, r, roomID, cursor, limit-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, views...)
		cursor = scanned
		if !more {
			break
		}
	}
	return out, nil
}

// Catchup returns up to limit messages with seq > after that the caller
// may see, the highest sequence it scanned (to continue from), and whether
// more rows may follow. The gateway uses it to fill gaps and to resume.
func (s *Service) Catchup(ctx context.Context, p auth.Principal, roomID uuid.UUID, after int64, limit int) (views []model.MessageView, scanned int64, more bool, err error) {
	reader, err := memberOf(p)
	if err != nil {
		return nil, after, false, err
	}
	if limit <= 0 || limit > model.PageMax {
		limit = model.PageMax
	}
	return s.catchup(ctx, s.newRenderer(reader), roomID, after, limit)
}

func (s *Service) catchup(ctx context.Context, r *renderer, roomID uuid.UUID, after int64, limit int) ([]model.MessageView, int64, bool, error) {
	raw, err := s.page(ctx, func(tx port.Tx) ([]model.Message, error) {
		return tx.ListMessagesAfter(ctx, roomID, after, int32(limit)) //nolint:gosec // at most model.PageMax
	})
	if err != nil {
		return nil, after, false, err
	}
	out := make([]model.MessageView, 0, len(raw))
	scanned := after
	for _, m := range raw {
		scanned = m.Seq
		v, ok, err := r.render(ctx, s, m)
		if err != nil {
			return nil, after, false, err
		}
		if ok {
			out = append(out, v)
		}
	}
	return out, scanned, len(raw) == limit, nil
}

func (s *Service) page(ctx context.Context, fn func(tx port.Tx) ([]model.Message, error)) ([]model.Message, error) {
	var out []model.Message
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	return out, err
}

// renderer renders messages for one reader, caching author names and block
// lookups for the duration of one request.
type renderer struct {
	reader  uuid.UUID
	names   map[uuid.UUID]string
	blocked map[uuid.UUID]bool
}

func (s *Service) newRenderer(reader uuid.UUID) *renderer {
	return &renderer{reader: reader, names: map[uuid.UUID]string{}, blocked: map[uuid.UUID]bool{}}
}

func (r *renderer) render(ctx context.Context, s *Service, m model.Message) (model.MessageView, bool, error) {
	if m.Status == model.StatusHeld && m.AuthorID != r.reader {
		return model.MessageView{}, false, nil
	}
	b, ok := r.blocked[m.AuthorID]
	if !ok {
		var err error
		if b, err = s.blocked(ctx, r.reader, m.AuthorID); err != nil {
			return model.MessageView{}, false, err
		}
		r.blocked[m.AuthorID] = b
	}
	if b {
		return model.MessageView{}, false, nil
	}
	name, ok := r.names[m.AuthorID]
	if !ok {
		ps, err := s.profiles(ctx, m.AuthorID)
		if err != nil {
			return model.MessageView{}, false, err
		}
		name = ps[m.AuthorID].FirstName
		r.names[m.AuthorID] = name
	}
	o := model.Opened{Message: m, AuthorName: name}
	if m.Status != model.StatusHidden {
		text, deleted, err := s.open(ctx, m)
		if err != nil {
			return model.MessageView{}, false, err
		}
		o.PlainText, o.Deleted = text, deleted
	}
	v, ok := model.View(o, r.reader)
	return v, ok, nil
}

// LoadOpened reads one message, opens its text and resolves its author's
// name once, for the gateway to render it for every connected reader.
func (s *Service) LoadOpened(ctx context.Context, messageID uuid.UUID) (model.Opened, error) {
	var m model.Message
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		m, err = tx.GetMessage(ctx, messageID, false)
		return err
	}); err != nil {
		return model.Opened{}, err
	}
	o := model.Opened{Message: m}
	if m.Status != model.StatusHidden {
		text, deleted, err := s.open(ctx, m)
		if err != nil {
			return model.Opened{}, err
		}
		o.PlainText, o.Deleted = text, deleted
	}
	ps, err := s.profiles(ctx, m.AuthorID)
	if err != nil {
		return model.Opened{}, err
	}
	o.AuthorName = ps[m.AuthorID].FirstName
	return o, nil
}

// Render returns o as reader sees it, or ok=false when she must not see it
// (held for someone else, or blocked either way). Block lookups fail closed.
func (s *Service) Render(ctx context.Context, reader uuid.UUID, o model.Opened) (model.MessageView, bool, error) {
	if o.Status == model.StatusHeld && o.AuthorID != reader {
		return model.MessageView{}, false, nil
	}
	b, err := s.blocked(ctx, reader, o.AuthorID)
	if err != nil {
		return model.MessageView{}, false, err
	}
	if b {
		return model.MessageView{}, false, nil
	}
	v, ok := model.View(o, reader)
	return v, ok, nil
}
