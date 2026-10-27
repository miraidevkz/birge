package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/platform/auth"
)

var (
	topicCodeRe = regexp.MustCompile(`^[a-z_]{2,40}$`)
	titleKeyRe  = regexp.MustCompile(`^[a-z0-9_.]{3,80}$`)
)

func secondsDur(n int) time.Duration { return time.Duration(n) * time.Second }

// EnsureDistrictRoom returns the district's room, creating it on first use
// (reference data seeding). Idempotent, also under concurrency.
func (s *Service) EnsureDistrictRoom(ctx context.Context, districtID uuid.UUID, titleKey string) (model.Room, error) {
	if districtID == uuid.Nil || !titleKeyRe.MatchString(titleKey) {
		return model.Room{}, model.ErrInvalidInput
	}
	d := districtID
	return s.ensureGroupRoom(ctx, model.Room{Kind: model.KindDistrict, DistrictID: &d, TitleKey: titleKey},
		func(tx port.Tx) (model.Room, error) { return tx.GetDistrictRoom(ctx, districtID) })
}

// EnsureTopicRoom returns the topic room of a needs code, creating it on
// first use. Idempotent, also under concurrency.
func (s *Service) EnsureTopicRoom(ctx context.Context, code, titleKey string) (model.Room, error) {
	if !topicCodeRe.MatchString(code) || !titleKeyRe.MatchString(titleKey) {
		return model.Room{}, model.ErrInvalidInput
	}
	return s.ensureGroupRoom(ctx, model.Room{Kind: model.KindTopic, TopicCode: code, TitleKey: titleKey},
		func(tx port.Tx) (model.Room, error) { return tx.GetTopicRoom(ctx, code) })
}

func (s *Service) ensureGroupRoom(ctx context.Context, r model.Room, get func(port.Tx) (model.Room, error)) (model.Room, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return model.Room{}, fmt.Errorf("chat: room id: %w", err)
	}
	r.ID, r.CreatedAt = id, s.now()
	var out model.Room
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		created, inserted, err := tx.InsertGroupRoom(ctx, r)
		if err != nil {
			return err
		}
		if inserted {
			out = created
			return nil
		}
		out, err = get(tx)
		return err
	})
	return out, err
}

// OpenDirectRoom opens the private room of a and b for an origin event
// (tea match, accepted SOS response, accepted listing contact). It is
// idempotent twice over: the event id is recorded in
// platform.processed_events in the same transaction, and the room has a
// natural key (origin, origin id, pair), so a redelivered or re-published
// event opens nothing new. Blocks are not checked here (that would make
// accepting the event depend on membership); messages between blocked
// members are never shown, whatever room they are in.
func (s *Service) OpenDirectRoom(ctx context.Context, eventID uuid.UUID, origin string, originID, a, b uuid.UUID) (model.Room, error) {
	switch origin {
	case model.OriginMatch, model.OriginSos, model.OriginListing:
	default:
		return model.Room{}, fmt.Errorf("%w: origin", model.ErrInvalidInput)
	}
	if eventID == uuid.Nil || originID == uuid.Nil || a == uuid.Nil || b == uuid.Nil || a == b {
		return model.Room{}, fmt.Errorf("%w: direct room members", model.ErrInvalidInput)
	}
	low, high := model.OrderPair(a, b)
	id, err := uuid.NewV7()
	if err != nil {
		return model.Room{}, fmt.Errorf("chat: room id: %w", err)
	}
	now := s.now()
	var out model.Room
	var created bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		first, err := tx.MarkProcessed(ctx, model.ConsumerDirectRooms, eventID)
		if err != nil {
			return err
		}
		if !first {
			out, err = tx.GetDirectRoom(ctx, origin, originID, low, high)
			return err
		}
		oid, l, h := originID, low, high
		room, inserted, err := tx.InsertDirectRoom(ctx, model.Room{
			ID: id, Kind: model.KindDirect, Origin: origin, OriginID: &oid, MemberLow: &l, MemberHigh: &h, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !inserted {
			out, err = tx.GetDirectRoom(ctx, origin, originID, low, high)
			return err
		}
		created, out = true, room
		for _, m := range []uuid.UUID{low, high} {
			if err := tx.AddRoomMemberIfAbsent(ctx, room.ID, m, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && created {
		s.log().Info("chat direct room opened", "event", "ChatDirectRoomOpened", "room_id", out.ID, "origin", origin, "event_id", eventID)
	}
	return out, err
}

// HandleRoomEvent opens the direct room for one consumed event envelope
// (circle.MatchAccepted, emergency.SosResponseAccepted,
// sharing.ContactAccepted). Other types are ignored.
func (s *Service) HandleRoomEvent(ctx context.Context, eventID uuid.UUID, eventType string, data json.RawMessage) error {
	var origin string
	var originID, a, b uuid.UUID
	switch eventType {
	case model.EventMatchAccepted:
		var d model.MatchAcceptedData
		if err := json.Unmarshal(data, &d); err != nil {
			return fmt.Errorf("chat: parse %s: %w", eventType, err)
		}
		origin, originID, a, b = model.OriginMatch, d.MatchID, d.MemberA, d.MemberB
	case model.EventSosResponseAccepted:
		var d model.SosResponseAcceptedData
		if err := json.Unmarshal(data, &d); err != nil {
			return fmt.Errorf("chat: parse %s: %w", eventType, err)
		}
		origin, originID, a, b = model.OriginSos, d.SosID, d.AuthorID, d.ResponderID
	case model.EventContactAccepted:
		var d model.ContactAcceptedData
		if err := json.Unmarshal(data, &d); err != nil {
			return fmt.Errorf("chat: parse %s: %w", eventType, err)
		}
		origin, originID, a, b = model.OriginListing, d.ListingID, d.AuthorID, d.RequesterID
	default:
		return nil
	}
	_, err := s.OpenDirectRoom(ctx, eventID, origin, originID, a, b)
	return err
}

// ListRooms returns the caller's rooms (joined = true), the group rooms she
// may join (joined = false: verified members only; her district's room and
// every topic room), or both (joined unset). kind filters by room kind.
func (s *Service) ListRooms(ctx context.Context, p auth.Principal, kind string, joined *bool) ([]model.RoomView, error) {
	member, err := memberOf(p)
	if err != nil {
		return nil, err
	}
	if kind != "" && kind != model.KindDistrict && kind != model.KindTopic && kind != model.KindDirect {
		return nil, model.ErrInvalidInput
	}
	wantMine := joined == nil || *joined
	wantJoinable := (joined == nil || !*joined) && kind != model.KindDirect
	var profile model.Profile
	if wantJoinable {
		ps, err := s.profiles(ctx, member)
		if err != nil {
			return nil, err
		}
		profile = ps[member]
	}
	var mine []model.MemberRoom
	var joinable []model.Room
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		if mine, err = tx.ListMemberRooms(ctx, member); err != nil {
			return err
		}
		if wantJoinable && profile.Verified {
			joinable, err = tx.ListJoinableGroupRooms(ctx, profile.DistrictID)
		}
		return err
	}); err != nil {
		return nil, err
	}
	in := map[uuid.UUID]bool{}
	out := []model.RoomView{}
	var others []uuid.UUID
	for _, r := range mine {
		in[r.ID] = true
		if r.Kind == model.KindDirect {
			others = append(others, r.Other(member))
		}
	}
	names := map[uuid.UUID]model.Profile{}
	if wantMine && len(others) > 0 && (kind == "" || kind == model.KindDirect) {
		if names, err = s.profiles(ctx, others...); err != nil {
			return nil, err
		}
	}
	if wantMine {
		for _, r := range mine {
			if kind != "" && r.Kind != kind {
				continue
			}
			v := roomView(r.Room, true, r.Muted)
			if r.Kind == model.KindDirect {
				v.Title = names[r.Other(member)].FirstName
			}
			out = append(out, v)
		}
	}
	for _, r := range joinable {
		if in[r.ID] || (kind != "" && r.Kind != kind) {
			continue
		}
		out = append(out, roomView(r, false, false))
	}
	return out, nil
}

func roomView(r model.Room, joined, muted bool) model.RoomView {
	return model.RoomView{ID: r.ID, Kind: r.Kind, Title: r.TitleKey, Joined: joined, Muted: muted, LastSeq: r.LastSeq, SlowModeSeconds: r.SlowModeSeconds}
}

// JoinRoom adds the caller to a group room (ADR-0016: never automatic).
// She must be verified; a district room only for her own district. A
// direct room can be (re)joined only by its two members. Banned → ErrBanned.
func (s *Service) JoinRoom(ctx context.Context, p auth.Principal, roomID uuid.UUID) (model.RoomView, error) {
	member, err := memberOf(p)
	if err != nil {
		return model.RoomView{}, err
	}
	var room model.Room
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		room, err = tx.GetRoom(ctx, roomID, false)
		return err
	}); err != nil {
		return model.RoomView{}, err
	}
	var title string
	switch room.Kind {
	case model.KindDirect:
		if !room.DirectMember(member) {
			return model.RoomView{}, model.ErrNotFound
		}
		ps, err := s.profiles(ctx, room.Other(member))
		if err != nil {
			return model.RoomView{}, err
		}
		title = ps[room.Other(member)].FirstName
	default:
		ps, err := s.profiles(ctx, member)
		if err != nil {
			return model.RoomView{}, err
		}
		prof, ok := ps[member]
		if !ok || !prof.Verified {
			return model.RoomView{}, model.ErrForbidden
		}
		if room.Kind == model.KindDistrict && (prof.DistrictID == nil || room.DistrictID == nil || *prof.DistrictID != *room.DistrictID) {
			return model.RoomView{}, model.ErrForbidden
		}
		title = room.TitleKey
	}
	var muted bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		r, err := tx.GetRestriction(ctx, roomID, member)
		if err != nil {
			return err
		}
		if r.BannedAt != nil {
			return model.ErrBanned
		}
		if err := tx.JoinRoom(ctx, roomID, member, s.now()); err != nil {
			return err
		}
		m, err := tx.GetRoomMember(ctx, roomID, member)
		muted = m.Muted
		if err == nil {
			room, err = tx.GetRoom(ctx, roomID, false)
		}
		return err
	})
	if err != nil {
		return model.RoomView{}, err
	}
	v := roomView(room, true, muted)
	v.Title = title
	return v, nil
}

// LeaveRoom removes the caller from the room (idempotent). Her gateway
// subscriptions to the room end through a "left" notice.
func (s *Service) LeaveRoom(ctx context.Context, p auth.Principal, roomID uuid.UUID) error {
	member, err := memberOf(p)
	if err != nil {
		return err
	}
	var left bool
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.GetRoom(ctx, roomID, false); err != nil {
			return err
		}
		left, err = tx.LeaveRoom(ctx, roomID, member, s.now())
		return err
	}); err != nil {
		return err
	}
	if left {
		m := member
		s.notify(ctx, model.Notice{Kind: model.NoticeLeft, RoomID: roomID, MemberID: &m})
	}
	return nil
}

// SetMute sets the caller's push mute for a room she is in.
func (s *Service) SetMute(ctx context.Context, p auth.Principal, roomID uuid.UUID, muted bool) error {
	member, err := memberOf(p)
	if err != nil {
		return err
	}
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.GetRoom(ctx, roomID, false); err != nil {
			return err
		}
		ok, err := tx.SetMute(ctx, roomID, member, muted)
		if err != nil {
			return err
		}
		if !ok {
			return model.ErrNotMember
		}
		return nil
	})
}

// CanSubscribe checks that the caller may receive the room's messages (she
// is in the room).
func (s *Service) CanSubscribe(ctx context.Context, p auth.Principal, roomID uuid.UUID) error {
	member, err := memberOf(p)
	if err != nil {
		return err
	}
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		_, err := activeMember(ctx, tx, roomID, member)
		return err
	})
}

// TouchPresence marks the caller connected to the rooms for PresenceTTL
// (gateway heartbeat), or disconnected when connected is false. Presence
// only suppresses pushes; an error is harmless and returned for logging.
func (s *Service) TouchPresence(ctx context.Context, memberID uuid.UUID, roomIDs []uuid.UUID, connected bool) error {
	if len(roomIDs) == 0 {
		return nil
	}
	until := s.now()
	if connected {
		until = until.Add(s.presenceTTL())
	}
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		for _, id := range roomIDs {
			if err := tx.TouchPresence(ctx, id, memberID, until); err != nil {
				return err
			}
		}
		return nil
	})
}

// PresenceInterval is how often the gateway should refresh presence.
func (s *Service) PresenceInterval() time.Duration { return s.presenceTTL() / 3 }
