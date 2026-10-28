package service_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/auth"
)

func TestEnsureGroupRoomValidationAndIdempotency(t *testing.T) {
	e := newEnv(t, service.Settings{})
	district := uuid.New()

	invalid := []struct {
		name string
		call func() error
	}{
		{"nil district", func() error { _, err := e.svc.EnsureDistrictRoom(e.ctx, uuid.Nil, "chat.room.d"); return err }},
		{"district title key with spaces", func() error { _, err := e.svc.EnsureDistrictRoom(e.ctx, district, "bad key"); return err }},
		{"topic code upper case", func() error { _, err := e.svc.EnsureTopicRoom(e.ctx, "Autism", "chat.room.t"); return err }},
		{"topic code too short", func() error { _, err := e.svc.EnsureTopicRoom(e.ctx, "a", "chat.room.t"); return err }},
		{"topic title key too short", func() error { _, err := e.svc.EnsureTopicRoom(e.ctx, "autism", "ab"); return err }},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, model.ErrInvalidInput) {
				t.Fatalf("want invalid input, got %v", err)
			}
		})
	}

	first := e.districtRoom(district)
	again, err := e.svc.EnsureDistrictRoom(e.ctx, district, "chat.room.district.other")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.TitleKey != first.TitleKey {
		t.Fatalf("second ensure changed the room: %+v vs %+v", again, first)
	}
	topic := e.topicRoom("autism")
	if topic2 := e.topicRoom("autism"); topic2.ID != topic.ID {
		t.Fatal("topic room created twice")
	}
}

func TestEnsureGroupRoomsCountsOnlyNewRooms(t *testing.T) {
	e := newEnv(t, service.Settings{})
	d1, d2 := uuid.New(), uuid.New()
	e.districtRoom(d1)
	res, err := e.svc.EnsureGroupRooms(e.ctx,
		[]service.DistrictRoomRef{{DistrictID: d1, TitleKey: "chat.room.d1"}, {DistrictID: d2, TitleKey: "chat.room.d2"}},
		[]service.TopicRoomRef{{Code: "autism", TitleKey: "chat.room.t.autism"}, {Code: "down_syndrome", TitleKey: "chat.room.t.down"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.DistrictsCreated != 1 || res.TopicsCreated != 2 {
		t.Fatalf("got %+v, want 1 district and 2 topics created", res)
	}
	res, err = e.svc.EnsureGroupRooms(e.ctx, []service.DistrictRoomRef{{DistrictID: d2, TitleKey: "chat.room.d2"}},
		[]service.TopicRoomRef{{Code: "autism", TitleKey: "chat.room.t.autism"}})
	if err != nil || res != (service.EnsureGroupRoomsResult{}) {
		t.Fatalf("rerun: %+v %v", res, err)
	}

	// A failure stops the run and reports what was created before it.
	if _, err := e.svc.EnsureGroupRooms(e.ctx, nil, []service.TopicRoomRef{{Code: "BAD", TitleKey: "chat.room.t"}}); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("invalid topic: %v", err)
	}
	if _, err := e.svc.EnsureGroupRooms(e.ctx, []service.DistrictRoomRef{{DistrictID: uuid.Nil, TitleKey: "chat.room.d"}}, nil); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("invalid district: %v", err)
	}
	down := errors.New("database down")
	e.store.FailOn("InsertGroupRoom", down)
	res, err = e.svc.EnsureGroupRooms(e.ctx, nil, []service.TopicRoomRef{{Code: "cerebral_palsy", TitleKey: "chat.room.t.cp"}})
	if !errors.Is(err, down) || res.TopicsCreated != 0 {
		t.Fatalf("store failure: %+v %v", res, err)
	}
}

func TestOpenDirectRoomRulesAndIdempotency(t *testing.T) {
	e := newEnv(t, service.Settings{})
	a, b := e.member(true, nil), e.member(true, nil)

	bad := []struct {
		name                  string
		event                 uuid.UUID
		origin                string
		originID, left, right uuid.UUID
	}{
		{"unknown origin", uuid.New(), "circle.other", uuid.New(), a.MemberID, b.MemberID},
		{"nil event", uuid.Nil, model.OriginMatch, uuid.New(), a.MemberID, b.MemberID},
		{"nil origin id", uuid.New(), model.OriginMatch, uuid.Nil, a.MemberID, b.MemberID},
		{"nil member", uuid.New(), model.OriginSos, uuid.New(), uuid.Nil, b.MemberID},
		{"same member twice", uuid.New(), model.OriginListing, uuid.New(), a.MemberID, a.MemberID},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.svc.OpenDirectRoom(e.ctx, tt.event, tt.origin, tt.originID, tt.left, tt.right); !errors.Is(err, model.ErrInvalidInput) {
				t.Fatalf("want invalid input, got %v", err)
			}
		})
	}

	event, match := uuid.New(), uuid.New()
	room, err := e.svc.OpenDirectRoom(e.ctx, event, model.OriginMatch, match, a.MemberID, b.MemberID)
	if err != nil {
		t.Fatal(err)
	}
	if room.Kind != model.KindDirect || !room.DirectMember(a.MemberID) || !room.DirectMember(b.MemberID) {
		t.Fatalf("room %+v", room)
	}
	for _, p := range []auth.Principal{a, b} {
		if err := e.svc.CanSubscribe(e.ctx, p, room.ID); err != nil {
			t.Fatalf("member not added: %v", err)
		}
	}

	// She leaves; a redelivered event and a re-published one (new event id,
	// pair in the other order) return the same room and do not bring her back.
	if err := e.svc.LeaveRoom(e.ctx, a, room.ID); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []uuid.UUID{event, uuid.New()} {
		again, err := e.svc.OpenDirectRoom(e.ctx, ev, model.OriginMatch, match, b.MemberID, a.MemberID)
		if err != nil || again.ID != room.ID {
			t.Fatalf("replay %v: room %v err %v", ev, again.ID, err)
		}
	}
	if err := e.svc.CanSubscribe(e.ctx, a, room.ID); !errors.Is(err, model.ErrNotMember) {
		t.Fatalf("left member re-added: %v", err)
	}

	// Another origin for the same pair is another room.
	sos := e.directRoom(model.OriginSos, a, b)
	if sos.ID == room.ID {
		t.Fatal("SOS room reused the match room")
	}

	down := errors.New("database down")
	e.store.FailOn("MarkProcessed", down)
	if _, err := e.svc.OpenDirectRoom(e.ctx, uuid.New(), model.OriginListing, uuid.New(), a.MemberID, b.MemberID); !errors.Is(err, down) {
		t.Fatalf("store failure: %v", err)
	}
}

func TestOpenDirectRoomRollsBackWhenAMemberCannotBeAdded(t *testing.T) {
	e := newEnv(t, service.Settings{})
	a, b := e.member(true, nil), e.member(true, nil)
	event, match := uuid.New(), uuid.New()
	e.store.FailOn("AddRoomMemberIfAbsent", errors.New("database down"))
	if _, err := e.svc.OpenDirectRoom(e.ctx, event, model.OriginMatch, match, a.MemberID, b.MemberID); err == nil {
		t.Fatal("want error")
	}
	e.store.FailOn("AddRoomMemberIfAbsent", nil)
	// The event was not marked processed: the redelivery opens the room.
	room, err := e.svc.OpenDirectRoom(e.ctx, event, model.OriginMatch, match, a.MemberID, b.MemberID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CanSubscribe(e.ctx, b, room.ID); err != nil {
		t.Fatalf("redelivery did not add the members: %v", err)
	}
}

func TestHandleRoomEventByType(t *testing.T) {
	e := newEnv(t, service.Settings{})
	a, b := e.member(true, nil), e.member(true, nil)
	raw := func(v any) json.RawMessage {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	cases := []struct {
		eventType string
		data      json.RawMessage
	}{
		{model.EventMatchAccepted, raw(model.MatchAcceptedData{MatchID: uuid.New(), MemberA: a.MemberID, MemberB: b.MemberID})},
		{model.EventSosResponseAccepted, raw(model.SosResponseAcceptedData{SosID: uuid.New(), AuthorID: a.MemberID, ResponderID: b.MemberID})},
		{model.EventContactAccepted, raw(model.ContactAcceptedData{ListingID: uuid.New(), AuthorID: a.MemberID, RequesterID: b.MemberID})},
	}
	for _, c := range cases {
		t.Run(c.eventType, func(t *testing.T) {
			if err := e.svc.HandleRoomEvent(e.ctx, uuid.New(), c.eventType, c.data); err != nil {
				t.Fatal(err)
			}
			if err := e.svc.HandleRoomEvent(e.ctx, uuid.New(), c.eventType, json.RawMessage(`{"broken`)); err == nil {
				t.Fatal("malformed payload accepted")
			}
		})
	}
	rooms, err := e.svc.ListRooms(e.ctx, a, model.KindDirect, nil)
	if err != nil || len(rooms) != 3 {
		t.Fatalf("want 3 direct rooms (one per origin), got %d %v", len(rooms), err)
	}
	if err := e.svc.HandleRoomEvent(e.ctx, uuid.New(), "circle.Other", json.RawMessage(`not json`)); err != nil {
		t.Fatalf("other event types are ignored, got %v", err)
	}
	// Missing members in the payload are invalid, not a room with nil ids.
	if err := e.svc.HandleRoomEvent(e.ctx, uuid.New(), model.EventMatchAccepted, raw(model.MatchAcceptedData{MatchID: uuid.New()})); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("got %v", err)
	}
}

func TestListRoomsVisibility(t *testing.T) {
	e := newEnv(t, service.Settings{})
	home, other := uuid.New(), uuid.New()
	homeRoom, otherRoom := e.districtRoom(home), e.districtRoom(other)
	topic := e.topicRoom("autism")
	mother := e.member(true, &home)
	friend := e.member(true, &home)
	e.members.Add(friend.MemberID, model.Profile{Verified: true, FirstName: "Fakefriend", DistrictID: &home})
	newcomer := e.member(false, &home)
	direct := e.directRoom(model.OriginMatch, mother, friend)
	e.join(mother, topic.ID)

	byID := func(vs []model.RoomView) map[uuid.UUID]model.RoomView {
		out := map[uuid.UUID]model.RoomView{}
		for _, v := range vs {
			out[v.ID] = v
		}
		return out
	}
	yes, no := true, false

	all, err := e.svc.ListRooms(e.ctx, mother, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := byID(all)
	if len(got) != 3 || !got[topic.ID].Joined || !got[direct.ID].Joined || got[homeRoom.ID].Joined {
		t.Fatalf("all rooms: %+v", all)
	}
	if _, ok := got[otherRoom.ID]; ok {
		t.Fatal("another district's room offered")
	}
	if got[direct.ID].Title != "Fakefriend" {
		t.Fatalf("direct room title %q, want the other member's first name", got[direct.ID].Title)
	}

	mine, err := e.svc.ListRooms(e.ctx, mother, "", &yes)
	if err != nil || len(mine) != 2 {
		t.Fatalf("joined only: %+v %v", mine, err)
	}
	joinable, err := e.svc.ListRooms(e.ctx, mother, "", &no)
	if err != nil || len(joinable) != 1 || joinable[0].ID != homeRoom.ID {
		t.Fatalf("joinable only: %+v %v", joinable, err)
	}
	topics, err := e.svc.ListRooms(e.ctx, mother, model.KindTopic, nil)
	if err != nil || len(topics) != 1 || topics[0].ID != topic.ID {
		t.Fatalf("kind topic: %+v %v", topics, err)
	}
	directs, err := e.svc.ListRooms(e.ctx, mother, model.KindDirect, &no)
	if err != nil || len(directs) != 0 {
		t.Fatalf("direct rooms are never joinable: %+v %v", directs, err)
	}

	// Unverified members see only rooms they are in: nothing to join.
	nv, err := e.svc.ListRooms(e.ctx, newcomer, "", nil)
	if err != nil || len(nv) != 0 {
		t.Fatalf("unverified: %+v %v", nv, err)
	}

	if _, err := e.svc.ListRooms(e.ctx, mother, "secret", nil); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("unknown kind: %v", err)
	}
	for _, p := range []auth.Principal{{}, staff("admin")} {
		if _, err := e.svc.ListRooms(e.ctx, p, "", nil); !errors.Is(err, model.ErrUnauthorized) {
			t.Fatalf("%+v: %v", p, err)
		}
	}

	// Membership down: no guessing who is verified.
	e.members.Err = errors.New("membership down")
	if _, err := e.svc.ListRooms(e.ctx, mother, "", nil); err == nil {
		t.Fatal("want error while membership is down")
	}
	e.members.Err = nil
	e.store.FailOn("ListMemberRooms", errors.New("database down"))
	if _, err := e.svc.ListRooms(e.ctx, mother, "", &yes); err == nil {
		t.Fatal("want store error")
	}
}

func TestStaffListRoomsDenyByDefault(t *testing.T) {
	e := newEnv(t, service.Settings{})
	topic := e.topicRoom("autism")
	mentor := e.member(true, nil)
	e.members.Add(mentor.MemberID, model.Profile{Verified: true, FirstName: "Fakementor"})
	e.members.SetMentor(mentor.MemberID, true)
	e.join(mentor, topic.ID)
	e.join(e.member(true, nil), topic.ID)
	e.directRoom(model.OriginMatch, e.member(true, nil), e.member(true, nil))
	admin := staff("admin")
	if err := e.svc.AssignMentor(e.ctx, admin, topic.ID, mentor.MemberID, true); err != nil {
		t.Fatal(err)
	}

	denied := map[string]auth.Principal{
		"member":             mentor,
		"staff without role": staff("support"),
		"staff no identity":  {Staff: true, Roles: []string{"admin"}},
		"anonymous":          {},
	}
	for name, p := range denied {
		t.Run(name, func(t *testing.T) {
			if _, err := e.svc.StaffListRooms(e.ctx, p); !errors.Is(err, model.ErrForbidden) {
				t.Fatalf("want forbidden, got %v", err)
			}
		})
	}

	for _, p := range []auth.Principal{admin, staff("moderator")} {
		rooms, err := e.svc.StaffListRooms(e.ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if len(rooms) != 1 || rooms[0].ID != topic.ID || rooms[0].MemberCount != 2 {
			t.Fatalf("rooms %+v (direct rooms must not be listed)", rooms)
		}
		if len(rooms[0].Mentors) != 1 || rooms[0].Mentors[0].MemberID != mentor.MemberID || rooms[0].Mentors[0].DisplayName != "Fakementor" {
			t.Fatalf("mentors %+v", rooms[0].Mentors)
		}
	}

	e.members.Err = errors.New("membership down")
	if _, err := e.svc.StaffListRooms(e.ctx, admin); err == nil {
		t.Fatal("want error while membership is down")
	}
	e.members.Err = nil
	e.store.FailOn("ListStaffRooms", errors.New("database down"))
	if _, err := e.svc.StaffListRooms(e.ctx, admin); err == nil {
		t.Fatal("want store error")
	}
}

func TestJoinRoomRules(t *testing.T) {
	e := newEnv(t, service.Settings{})
	home, other := uuid.New(), uuid.New()
	homeRoom, otherRoom := e.districtRoom(home), e.districtRoom(other)
	topic := e.topicRoom("autism")
	mother := e.member(true, &home)
	noDistrict := e.member(true, nil)
	unverified := e.member(false, &home)
	unknown := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()} // no profile (deleted)

	cases := []struct {
		name string
		p    auth.Principal
		room uuid.UUID
		want error
	}{
		{"verified joins a topic room", mother, topic.ID, nil},
		{"verified joins her district room", mother, homeRoom.ID, nil},
		{"not another district", mother, otherRoom.ID, model.ErrForbidden},
		{"no district set", noDistrict, homeRoom.ID, model.ErrForbidden},
		{"unverified", unverified, topic.ID, model.ErrForbidden},
		{"no profile", unknown, topic.ID, model.ErrForbidden},
		{"unknown room", mother, uuid.New(), model.ErrNotFound},
		{"staff", staff("admin"), topic.ID, model.ErrUnauthorized},
		{"anonymous", auth.Principal{}, topic.ID, model.ErrUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := e.svc.JoinRoom(e.ctx, c.p, c.room)
			if c.want == nil {
				if err != nil || !v.Joined || v.ID != c.room {
					t.Fatalf("join: %+v %v", v, err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}

	// Joining twice is fine; muting survives leaving and re-joining.
	if err := e.svc.SetMute(e.ctx, mother, topic.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.LeaveRoom(e.ctx, mother, topic.ID); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.JoinRoom(e.ctx, mother, topic.ID)
	if err != nil || !v.Muted || v.Title != topic.TitleKey {
		t.Fatalf("re-join: %+v %v", v, err)
	}

	// Membership down: fail closed.
	e.members.Err = errors.New("membership down")
	if _, err := e.svc.JoinRoom(e.ctx, mother, topic.ID); err == nil || errors.Is(err, model.ErrForbidden) {
		t.Fatalf("want an internal error, got %v", err)
	}
	e.members.Err = nil
	e.store.FailOn("JoinRoom", errors.New("database down"))
	if _, err := e.svc.JoinRoom(e.ctx, mother, homeRoom.ID); err == nil {
		t.Fatal("want store error")
	}
}

func TestJoinRoomBannedMemberStaysOut(t *testing.T) {
	e := newEnv(t, service.Settings{})
	topic := e.topicRoom("autism")
	mother := e.member(true, nil)
	e.join(mother, topic.ID)
	if err := e.svc.ModerateRoom(e.ctx, staff("moderator"), topic.ID, model.Moderation{Action: model.ActionBan, MemberID: &mother.MemberID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.JoinRoom(e.ctx, mother, topic.ID); !errors.Is(err, model.ErrBanned) {
		t.Fatalf("banned member re-joined: %v", err)
	}
	if err := e.svc.ModerateRoom(e.ctx, staff("moderator"), topic.ID, model.Moderation{Action: model.ActionUnban, MemberID: &mother.MemberID}); err != nil {
		t.Fatal(err)
	}
	e.join(mother, topic.ID)
}

func TestJoinDirectRoomOnlyItsMembers(t *testing.T) {
	e := newEnv(t, service.Settings{})
	a, b := e.member(true, nil), e.member(true, nil)
	e.members.Add(b.MemberID, model.Profile{Verified: true, FirstName: "Fakeother"})
	stranger := e.member(true, nil)
	room := e.directRoom(model.OriginSos, a, b)
	if err := e.svc.LeaveRoom(e.ctx, a, room.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.JoinRoom(e.ctx, stranger, room.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("a stranger learns of the direct room: %v", err)
	}
	v, err := e.svc.JoinRoom(e.ctx, a, room.ID)
	if err != nil || v.Title != "Fakeother" || v.Kind != model.KindDirect {
		t.Fatalf("re-join direct: %+v %v", v, err)
	}
	e.members.Err = errors.New("membership down")
	if _, err := e.svc.JoinRoom(e.ctx, a, room.ID); err == nil {
		t.Fatal("want error while membership is down")
	}
}

func TestLeaveMuteSubscribe(t *testing.T) {
	e := newEnv(t, service.Settings{})
	topic := e.topicRoom("autism")
	mother, outsider := e.member(true, nil), e.member(true, nil)
	e.join(mother, topic.ID)

	if err := e.svc.CanSubscribe(e.ctx, mother, topic.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CanSubscribe(e.ctx, outsider, topic.ID); !errors.Is(err, model.ErrNotMember) {
		t.Fatalf("outsider subscribe: %v", err)
	}
	if err := e.svc.CanSubscribe(e.ctx, mother, uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown room: %v", err)
	}
	if err := e.svc.CanSubscribe(e.ctx, staff("admin"), topic.ID); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("staff subscribe: %v", err)
	}

	if err := e.svc.SetMute(e.ctx, outsider, topic.ID, true); !errors.Is(err, model.ErrNotMember) {
		t.Fatalf("outsider mute: %v", err)
	}
	if err := e.svc.SetMute(e.ctx, mother, uuid.New(), true); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("mute unknown room: %v", err)
	}
	if err := e.svc.SetMute(e.ctx, auth.Principal{}, topic.ID, true); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("anonymous mute: %v", err)
	}
	e.store.FailOn("SetMute", errors.New("database down"))
	if err := e.svc.SetMute(e.ctx, mother, topic.ID, true); err == nil {
		t.Fatal("want store error")
	}
	e.store.FailOn("SetMute", nil)

	// Leaving twice is fine; only the first sends the "left" notice that
	// ends her live subscriptions.
	for range 2 {
		if err := e.svc.LeaveRoom(e.ctx, mother, topic.ID); err != nil {
			t.Fatal(err)
		}
	}
	var left int
	for _, n := range e.bus.Notices() {
		if n.Kind == model.NoticeLeft && n.MemberID != nil && *n.MemberID == mother.MemberID {
			left++
		}
	}
	if left != 1 {
		t.Fatalf("left notices: %d", left)
	}
	if err := e.svc.CanSubscribe(e.ctx, mother, topic.ID); !errors.Is(err, model.ErrNotMember) {
		t.Fatalf("left member subscribe: %v", err)
	}
	if err := e.svc.LeaveRoom(e.ctx, mother, uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("leave unknown room: %v", err)
	}
	if err := e.svc.LeaveRoom(e.ctx, staff("admin"), topic.ID); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("staff leave: %v", err)
	}
}

func TestPresenceSuppressesPushUntilTTL(t *testing.T) {
	e := newEnv(t, service.Settings{PresenceTTL: 90 * time.Second})
	if got := e.svc.PresenceInterval(); got != 30*time.Second {
		t.Fatalf("presence interval %v", got)
	}
	topic := e.topicRoom("autism")
	author, reader := e.member(true, nil), e.member(true, nil)
	e.join(author, topic.ID)
	e.join(reader, topic.ID)

	if err := e.svc.TouchPresence(e.ctx, reader.MemberID, nil, true); err != nil {
		t.Fatalf("no rooms is a no-op: %v", err)
	}
	if err := e.svc.TouchPresence(e.ctx, reader.MemberID, []uuid.UUID{topic.ID}, true); err != nil {
		t.Fatal(err)
	}
	m := e.send(author, topic.ID, fakeText).Message
	if err := e.inTx(func(tx port.Tx) error { return e.svc.HandlePush(e.ctx, tx, m.ID) }); err != nil {
		t.Fatal(err)
	}
	if n := len(e.store.EventsOf(model.EventDeliveryRequested)); n != 0 {
		t.Fatalf("connected reader pushed: %d", n)
	}

	// Disconnected: the next message is pushed.
	if err := e.svc.TouchPresence(e.ctx, reader.MemberID, []uuid.UUID{topic.ID}, false); err != nil {
		t.Fatal(err)
	}
	m2 := e.send(author, topic.ID, fakeText).Message
	if err := e.inTx(func(tx port.Tx) error { return e.svc.HandlePush(e.ctx, tx, m2.ID) }); err != nil {
		t.Fatal(err)
	}
	if n := len(e.store.EventsOf(model.EventDeliveryRequested)); n != 1 {
		t.Fatalf("disconnected reader not pushed: %d", n)
	}

	e.store.FailOn("TouchPresence", errors.New("database down"))
	if err := e.svc.TouchPresence(e.ctx, reader.MemberID, []uuid.UUID{topic.ID}, true); err == nil {
		t.Fatal("want store error")
	}
}
