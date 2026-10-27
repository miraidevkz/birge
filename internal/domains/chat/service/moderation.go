package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/platform/auth"
)

// moderator is who acts: staff (full rights) or a room's mentor (limited).
type moderator struct {
	kind  string
	actor uuid.UUID
}

// moderatorFor resolves the caller's moderation rights in a room. Staff need
// the moderator or admin role; a member needs the membership role "mentor"
// (Mentors port, checked at every action) and an assignment to this room.
func (s *Service) moderatorFor(ctx context.Context, tx port.Tx, p auth.Principal, roomID uuid.UUID) (moderator, error) {
	if p.Staff {
		if !model.IsStaffModerator(p.Roles) || p.IdentityID == uuid.Nil {
			return moderator{}, model.ErrForbidden
		}
		return moderator{kind: model.ModeratorStaff, actor: p.IdentityID}, nil
	}
	if p.MemberID == uuid.Nil {
		return moderator{}, model.ErrUnauthorized
	}
	assigned, err := tx.IsRoomMentor(ctx, roomID, p.MemberID)
	if err != nil {
		return moderator{}, err
	}
	if !assigned {
		return moderator{}, model.ErrForbidden
	}
	if s.Mentors == nil {
		return moderator{}, errors.New("chat: mentors not configured")
	}
	ok, err := s.Mentors.IsMentor(ctx, p.MemberID)
	if err != nil {
		return moderator{}, fmt.Errorf("chat: mentor role: %w", err)
	}
	if !ok {
		return moderator{}, model.ErrForbidden
	}
	return moderator{kind: model.ModeratorMentor, actor: p.MemberID}, nil
}

// ModerateRoom applies one moderation action in a room. Staff may take
// every action; a mentor of the room only hide_message, restore_message and
// mute_1h (ADR-0016). Every applied action is audited in the same
// transaction. A crisis-held message is never restored to the room: the
// crisis signal stays private to the author and the escalation (ADR-0016).
func (s *Service) ModerateRoom(ctx context.Context, p auth.Principal, roomID uuid.UUID, in model.Moderation) error {
	if !model.ActionIsValid(in.Action) {
		return model.ErrInvalidInput
	}
	now := s.now()
	var notices []model.Notice
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		// Who may moderate is settled before the room is loaded, so a caller
		// who may not learns nothing about which rooms exist
		// (docs/solved-problems.md, "Authorize before the lookup").
		mod, err := s.moderatorFor(ctx, tx, p, roomID)
		if err != nil {
			return err
		}
		room, err := tx.GetRoom(ctx, roomID, true)
		if err != nil {
			return err
		}
		if !model.Allowed(mod.kind, in.Action) {
			return model.ErrForbidden
		}
		entry := model.AuditEntry{ActorID: mod.actor, ActorRole: mod.kind}
		switch in.Action {
		case model.ActionHideMessage, model.ActionRestoreMessage:
			if in.MessageID == nil {
				return model.ErrInvalidInput
			}
			m, err := tx.GetMessage(ctx, *in.MessageID, true)
			if err != nil {
				return err
			}
			if m.RoomID != room.ID {
				return model.ErrNotFound
			}
			status, action := model.StatusHidden, model.AuditHide
			if in.Action == model.ActionRestoreMessage {
				if m.HoldReason == model.HoldCrisis {
					return model.ErrConflict
				}
				status, action = model.StatusVisible, model.AuditRestore
			}
			entry.Action = action
			ns, err := applyMessageStatus(ctx, tx, m, status, entry, now)
			if err != nil {
				return err
			}
			notices = append(notices, ns...)
			return nil
		case model.ActionMute1h, model.ActionBan, model.ActionUnban:
			if in.MemberID == nil || *in.MemberID == uuid.Nil {
				return model.ErrInvalidInput
			}
			target := *in.MemberID
			switch in.Action {
			case model.ActionMute1h:
				err = tx.SetMutedUntil(ctx, room.ID, target, now.Add(model.MuteDuration), now)
				entry.Action = model.AuditMute
			case model.ActionBan:
				if err = tx.SetBan(ctx, room.ID, target, &now, now); err == nil {
					var left bool
					if left, err = tx.LeaveRoom(ctx, room.ID, target, now); left {
						notices = append(notices, model.Notice{Kind: model.NoticeLeft, RoomID: room.ID, MemberID: &target})
					}
				}
				entry.Action = model.AuditBan
			default:
				err = tx.SetBan(ctx, room.ID, target, nil, now)
				entry.Action = model.AuditUnban
			}
			if err != nil {
				return err
			}
			entry.ObjectType, entry.ObjectID = model.AuditObjectMember, target
			return tx.Audit(ctx, entry)
		case model.ActionSetSlowMode:
			if in.SlowModeSeconds == nil || *in.SlowModeSeconds < 0 || *in.SlowModeSeconds > model.MaxSlowModeSeconds {
				return model.ErrInvalidInput
			}
			if err := tx.SetSlowMode(ctx, room.ID, *in.SlowModeSeconds); err != nil {
				return err
			}
			entry.Action, entry.ObjectType, entry.ObjectID = model.AuditSlowMode, model.AuditObjectRoom, room.ID
			return tx.Audit(ctx, entry)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, n := range notices {
		s.notify(ctx, n)
	}
	s.log().Info("chat moderation", "event", "ChatModeration", "room_id", roomID, "action", in.Action)
	return nil
}

// AssignMentor assigns (assign = true) or removes a mentor of a group room.
// Staff only (moderator or admin); the member must hold the mentor role to
// be assigned. Audited.
func (s *Service) AssignMentor(ctx context.Context, p auth.Principal, roomID, memberID uuid.UUID, assign bool) error {
	if !p.Staff || !model.IsStaffModerator(p.Roles) || p.IdentityID == uuid.Nil {
		return model.ErrForbidden
	}
	if memberID == uuid.Nil {
		return model.ErrInvalidInput
	}
	if assign {
		if s.Mentors == nil {
			return errors.New("chat: mentors not configured")
		}
		ok, err := s.Mentors.IsMentor(ctx, memberID)
		if err != nil {
			return fmt.Errorf("chat: mentor role: %w", err)
		}
		if !ok {
			return model.ErrConflict
		}
	}
	now := s.now()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		room, err := tx.GetRoom(ctx, roomID, false)
		if err != nil {
			return err
		}
		if !room.Group() {
			return model.ErrConflict
		}
		action := model.AuditMentorAssign
		if assign {
			err = tx.AssignMentor(ctx, roomID, memberID, p.IdentityID, now)
		} else {
			action = model.AuditMentorRemove
			_, err = tx.RemoveMentor(ctx, roomID, memberID, now)
		}
		if err != nil {
			return err
		}
		return tx.Audit(ctx, model.AuditEntry{ActorID: p.IdentityID, ActorRole: model.ModeratorStaff, Action: action, ObjectType: model.AuditObjectMember, ObjectID: memberID})
	})
}

// ReportMessage records the caller's report of a message she can see and
// publishes chat.MessageReported once per reporter and message. The message
// is then kept past retention until its reports are closed.
func (s *Service) ReportMessage(ctx context.Context, p auth.Principal, messageID uuid.UUID, reason string) error {
	reporter, err := memberOf(p)
	if err != nil {
		return err
	}
	if !slices.Contains(model.ReportReasons, reason) {
		return model.ErrInvalidInput
	}
	var m model.Message
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		if m, err = tx.GetMessage(ctx, messageID, false); err != nil {
			return err
		}
		_, err = activeMember(ctx, tx, m.RoomID, reporter)
		return err
	}); err != nil {
		if errors.Is(err, model.ErrNotMember) {
			return model.ErrNotFound
		}
		return err
	}
	if m.AuthorID == reporter {
		return model.ErrInvalidInput
	}
	// Only a message she can see can be reported; anything else is "not found".
	if _, ok, err := s.newRenderer(reporter).render(ctx, s, m); err != nil {
		return err
	} else if !ok {
		return model.ErrNotFound
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("chat: report id: %w", err)
	}
	now := s.now()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		inserted, err := tx.InsertReport(ctx, id, m.ID, m.RoomID, reporter, reason, now)
		if err != nil || !inserted {
			return err
		}
		s.log().Info("chat message reported", "event", model.EventMessageReported, "message_id", m.ID)
		return tx.Publish(ctx, now, model.MessageReported(m.ID, m.RoomID, reason))
	})
}

// CloseReports closes the open reports of a message (staff, audited), which
// lets retention purge it again.
func (s *Service) CloseReports(ctx context.Context, p auth.Principal, messageID uuid.UUID) error {
	return s.staffMessageAction(ctx, p, messageID, model.AuditReportsClose, func(tx port.Tx, now time.Time, actor model.AuditEntry) error {
		return closeReportsInTx(ctx, tx, messageID, actor, now)
	})
}

// SetLegalHold puts a message under legal hold or releases it (staff,
// audited). A held message is kept outside retention (ADR-0019).
func (s *Service) SetLegalHold(ctx context.Context, p auth.Principal, messageID uuid.UUID, on bool) error {
	action := model.AuditLegalHoldOff
	if on {
		action = model.AuditLegalHoldOn
	}
	return s.staffMessageAction(ctx, p, messageID, action, func(tx port.Tx, _ time.Time, actor model.AuditEntry) error {
		return setLegalHoldInTx(ctx, tx, messageID, on, actor)
	})
}

func (s *Service) staffMessageAction(ctx context.Context, p auth.Principal, messageID uuid.UUID, action string, fn func(port.Tx, time.Time, model.AuditEntry) error) error {
	if !p.Staff || !model.IsStaffModerator(p.Roles) || p.IdentityID == uuid.Nil {
		return model.ErrForbidden
	}
	now := s.now()
	actor := model.AuditEntry{ActorID: p.IdentityID, ActorRole: model.ModeratorStaff, Action: action, ObjectType: model.AuditObjectMessage, ObjectID: messageID}
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.GetMessage(ctx, messageID, true); err != nil {
			return err
		}
		return fn(tx, now, actor)
	})
}

// ApplyModerationDecided applies moderation.Decided for kinds chat_message
// and report. Deduplicated via platform.processed_events. Audits use role
// system with ActorID = the moderation item id. A crisis-held message is
// never restored. block_author requires Suspenders and runs inside the same
// transaction so a membership failure redelivers the event.
func (s *Service) ApplyModerationDecided(ctx context.Context, eventID uuid.UUID, data model.ModerationDecidedData) error {
	switch data.Kind {
	case model.ModerationKindChatMessage, model.ModerationKindReport:
	default:
		return nil
	}
	if eventID == uuid.Nil || data.ObjectID == uuid.Nil || data.ItemID == uuid.Nil {
		return fmt.Errorf("%w: moderation event", model.ErrInvalidInput)
	}
	if data.BlockAuthor && s.Suspenders == nil {
		return errors.New("chat: author suspender not configured")
	}
	now := s.now()
	var notices []model.Notice
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		newly, err := tx.MarkProcessed(ctx, model.ConsumerModerationDecided, eventID)
		if err != nil {
			return err
		}
		if !newly {
			return nil
		}
		m, err := tx.GetMessage(ctx, data.ObjectID, true)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return nil
			}
			return err
		}
		system := func(action string) model.AuditEntry {
			return model.AuditEntry{
				ActorID: data.ItemID, ActorRole: model.ModeratorSystem,
				Action: action, ObjectType: model.AuditObjectMessage, ObjectID: m.ID,
			}
		}
		switch data.Kind {
		case model.ModerationKindChatMessage:
			if data.Approve {
				if m.HoldReason != model.HoldCrisis {
					ns, err := applyMessageStatus(ctx, tx, m, model.StatusVisible, system(model.AuditRestore), now)
					if err != nil {
						return err
					}
					notices = append(notices, ns...)
				}
			} else {
				ns, err := applyMessageStatus(ctx, tx, m, model.StatusHidden, system(model.AuditHide), now)
				if err != nil {
					return err
				}
				notices = append(notices, ns...)
			}
		case model.ModerationKindReport:
			if !data.Approve {
				ns, err := applyMessageStatus(ctx, tx, m, model.StatusHidden, system(model.AuditHide), now)
				if err != nil {
					return err
				}
				notices = append(notices, ns...)
			}
			if err := closeReportsInTx(ctx, tx, m.ID, system(model.AuditReportsClose), now); err != nil {
				return err
			}
		}
		if data.LegalHold {
			if err := setLegalHoldInTx(ctx, tx, m.ID, true, system(model.AuditLegalHoldOn)); err != nil {
				return err
			}
		}
		if data.BlockAuthor {
			// Call membership inside the chat transaction so a failure rolls
			// back MarkProcessed and the event is redelivered.
			if err := s.Suspenders.SuspendAuthor(ctx, m.AuthorID, data.Reason, data.ItemID); err != nil {
				return fmt.Errorf("chat: suspend author: %w", err)
			}
		}
		s.log().Info("chat moderation decided applied", "event", "ChatModerationDecidedApplied", "message_id", m.ID, "kind", data.Kind)
		return nil
	})
	if err != nil {
		return err
	}
	for _, n := range notices {
		s.notify(ctx, n)
	}
	return nil
}

// ModerationSummary returns the opened message text for the moderation
// ContentSource (kinds report and chat_message). Purged or gone messages
// yield model.ErrNotFound. Hidden messages still return text so staff can
// review them. Never includes contacts or profile fields.
func (s *Service) ModerationSummary(ctx context.Context, messageID uuid.UUID) (string, error) {
	var m model.Message
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		m, err = tx.GetMessage(ctx, messageID, false)
		return err
	}); err != nil {
		return "", err
	}
	text, deleted, err := s.open(ctx, m)
	if err != nil {
		return "", err
	}
	if deleted || text == "" {
		return "", model.ErrNotFound
	}
	return text, nil
}

// applyMessageStatus sets status when different, schedules push on held→visible,
// records a changed notice, and always audits the action.
func applyMessageStatus(ctx context.Context, tx port.Tx, m model.Message, status string, actor model.AuditEntry, now time.Time) ([]model.Notice, error) {
	var notices []model.Notice
	if m.Status != status {
		updated, err := tx.SetMessageStatus(ctx, m.ID, status, now)
		if err != nil {
			return nil, err
		}
		if m.Status == model.StatusHeld && status == model.StatusVisible {
			if _, err := tx.Schedule(ctx, model.TimerPush, m.ID, m.ID.String(), now); err != nil {
				return nil, err
			}
		}
		id := updated.ID
		notices = append(notices, model.Notice{Kind: model.NoticeChanged, RoomID: m.RoomID, MessageID: &id, Seq: updated.Seq})
	}
	actor.ObjectType, actor.ObjectID = model.AuditObjectMessage, m.ID
	if err := tx.Audit(ctx, actor); err != nil {
		return nil, err
	}
	return notices, nil
}

func closeReportsInTx(ctx context.Context, tx port.Tx, messageID uuid.UUID, actor model.AuditEntry, now time.Time) error {
	if _, err := tx.CloseReports(ctx, messageID, now); err != nil {
		return err
	}
	actor.Action, actor.ObjectType, actor.ObjectID = model.AuditReportsClose, model.AuditObjectMessage, messageID
	return tx.Audit(ctx, actor)
}

func setLegalHoldInTx(ctx context.Context, tx port.Tx, messageID uuid.UUID, on bool, actor model.AuditEntry) error {
	if _, err := tx.SetLegalHold(ctx, messageID, on); err != nil {
		return err
	}
	action := model.AuditLegalHoldOff
	if on {
		action = model.AuditLegalHoldOn
	}
	actor.Action, actor.ObjectType, actor.ObjectID = action, model.AuditObjectMessage, messageID
	return tx.Audit(ctx, actor)
}
