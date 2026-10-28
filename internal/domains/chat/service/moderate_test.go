package service_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/auth"
)

// modEnv is a topic room with an author, a reader and a mentor assigned to
// the room who holds the membership mentor role.
type modEnv struct {
	*env
	room                   model.Room
	author, reader, mentor auth.Principal
	msg                    model.MessageView
}

func newModEnv(t *testing.T) *modEnv {
	t.Helper()
	e := newEnv(t, service.Settings{})
	m := &modEnv{env: e, room: e.topicRoom("autism")}
	m.author, m.reader, m.mentor = e.member(true, nil), e.member(true, nil), e.member(true, nil)
	for _, p := range []auth.Principal{m.author, m.reader, m.mentor} {
		e.join(p, m.room.ID)
	}
	e.members.SetMentor(m.mentor.MemberID, true)
	if err := e.svc.AssignMentor(e.ctx, staff("admin"), m.room.ID, m.mentor.MemberID, true); err != nil {
		t.Fatal(err)
	}
	m.msg = e.send(m.author, m.room.ID, fakeText).Message
	return m
}

func (m *modEnv) lastAudit() model.AuditEntry {
	a := m.store.Audit()
	if len(a) == 0 {
		m.t.Fatal("no audit entry")
	}
	return a[len(a)-1]
}

func TestModerateRoomDenyByDefault(t *testing.T) {
	m := newModEnv(t)
	other := m.topicRoom("down_syndrome")
	hide := model.Moderation{Action: model.ActionHideMessage, MessageID: &m.msg.ID}
	revoked := m.member(true, nil)
	m.join(revoked, m.room.ID)
	if err := m.svc.AssignMentor(m.ctx, staff("admin"), m.room.ID, revoked.MemberID, false); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		p    auth.Principal
		room uuid.UUID
		in   model.Moderation
		want error
	}{
		{"plain member", m.reader, m.room.ID, hide, model.ErrForbidden},
		{"anonymous", auth.Principal{}, m.room.ID, hide, model.ErrUnauthorized},
		{"staff without a moderation role", staff("support"), m.room.ID, hide, model.ErrForbidden},
		{"staff without identity", auth.Principal{Staff: true, Roles: []string{"admin"}}, m.room.ID, hide, model.ErrForbidden},
		{"mentor of another room", m.mentor, other.ID, hide, model.ErrForbidden},
		{"member never assigned", revoked, m.room.ID, hide, model.ErrForbidden},
		{"mentor may not ban", m.mentor, m.room.ID, model.Moderation{Action: model.ActionBan, MemberID: &m.author.MemberID}, model.ErrForbidden},
		{"mentor may not unban", m.mentor, m.room.ID, model.Moderation{Action: model.ActionUnban, MemberID: &m.author.MemberID}, model.ErrForbidden},
		{"mentor may not set slow mode", m.mentor, m.room.ID, model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: new(int)}, model.ErrForbidden},
		{"unknown action", staff("admin"), m.room.ID, model.Moderation{Action: "delete_room"}, model.ErrInvalidInput},
		// Authorization comes before the room lookup: an unknown room tells a
		// plain member nothing.
		{"member, unknown room", m.reader, uuid.New(), hide, model.ErrForbidden},
		{"staff, unknown room", staff("admin"), uuid.New(), hide, model.ErrNotFound},
	}
	before := len(m.store.Audit())
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := m.svc.ModerateRoom(m.ctx, c.p, c.room, c.in); !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
	if len(m.store.Audit()) != before {
		t.Fatal("a refused action was audited")
	}
	if got, _ := m.store.Message(m.msg.ID); got.Status != model.StatusVisible {
		t.Fatalf("refused hide changed the message: %s", got.Status)
	}

	// The mentor role is checked at every action: once membership takes it,
	// the assignment alone is not enough.
	m.members.SetMentor(m.mentor.MemberID, false)
	if err := m.svc.ModerateRoom(m.ctx, m.mentor, m.room.ID, hide); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("mentor without the role: %v", err)
	}
	m.members.SetMentor(m.mentor.MemberID, true)
	m.members.Err = errors.New("membership down")
	if err := m.svc.ModerateRoom(m.ctx, m.mentor, m.room.ID, hide); err == nil || errors.Is(err, model.ErrForbidden) {
		t.Fatalf("membership down must be an error: %v", err)
	}
	m.members.Err = nil
	m.svc.Mentors = nil
	if err := m.svc.ModerateRoom(m.ctx, m.mentor, m.room.ID, hide); err == nil {
		t.Fatal("mentor accepted without a mentors port")
	}
}

func TestModerateRoomMentorActions(t *testing.T) {
	m := newModEnv(t)
	noticesBefore := len(m.bus.Notices())
	hide := model.Moderation{Action: model.ActionHideMessage, MessageID: &m.msg.ID}
	if err := m.svc.ModerateRoom(m.ctx, m.mentor, m.room.ID, hide); err != nil {
		t.Fatal(err)
	}
	if a := m.lastAudit(); a.ActorID != m.mentor.MemberID || a.ActorRole != model.ModeratorMentor || a.Action != model.AuditHide || a.ObjectID != m.msg.ID {
		t.Fatalf("audit %+v", a)
	}
	if got := m.list(m.reader, m.room.ID); len(got) != 1 || got[0].Status != model.StatusHidden || got[0].Text != nil {
		t.Fatalf("reader after hide: %+v", got)
	}
	notices := m.bus.Notices()[noticesBefore:]
	if len(notices) != 1 || notices[0].Kind != model.NoticeChanged || *notices[0].MessageID != m.msg.ID {
		t.Fatalf("notices %+v", notices)
	}
	// Hiding again changes nothing but is still audited (who looked at it).
	if err := m.svc.ModerateRoom(m.ctx, m.mentor, m.room.ID, hide); err != nil {
		t.Fatal(err)
	}
	if len(m.bus.Notices()) != noticesBefore+1 {
		t.Fatal("a no-op hide sent a notice")
	}

	restore := model.Moderation{Action: model.ActionRestoreMessage, MessageID: &m.msg.ID}
	if err := m.svc.ModerateRoom(m.ctx, m.mentor, m.room.ID, restore); err != nil {
		t.Fatal(err)
	}
	if got := m.list(m.reader, m.room.ID); got[0].Status != model.StatusVisible || *got[0].Text != fakeText {
		t.Fatalf("reader after restore: %+v", got)
	}
	if err := m.svc.ModerateRoom(m.ctx, m.mentor, m.room.ID, model.Moderation{Action: model.ActionMute1h, MemberID: &m.author.MemberID}); err != nil {
		t.Fatal(err)
	}
	if a := m.lastAudit(); a.Action != model.AuditMute || a.ObjectType != model.AuditObjectMember || a.ObjectID != m.author.MemberID {
		t.Fatalf("mute audit %+v", a)
	}
	if _, err := m.svc.Send(m.ctx, m.author, m.room.ID, clientID(), fakeText); !errors.Is(err, model.ErrBanned) {
		t.Fatalf("muted author posts: %v", err)
	}
}

func TestModerateRoomMessageChecks(t *testing.T) {
	m := newModEnv(t)
	admin := staff("admin")
	other := m.topicRoom("down_syndrome")
	m.join(m.author, other.ID)
	elsewhere := m.send(m.author, other.ID, fakeText).Message
	missing := uuid.New()

	cases := []struct {
		name string
		in   model.Moderation
		want error
	}{
		{"hide without message", model.Moderation{Action: model.ActionHideMessage}, model.ErrInvalidInput},
		{"hide unknown message", model.Moderation{Action: model.ActionHideMessage, MessageID: &missing}, model.ErrNotFound},
		{"hide a message of another room", model.Moderation{Action: model.ActionHideMessage, MessageID: &elsewhere.ID}, model.ErrNotFound},
		{"mute without member", model.Moderation{Action: model.ActionMute1h}, model.ErrInvalidInput},
		{"ban nil member", model.Moderation{Action: model.ActionBan, MemberID: &uuid.Nil}, model.ErrInvalidInput},
		{"slow mode missing", model.Moderation{Action: model.ActionSetSlowMode}, model.ErrInvalidInput},
		{"slow mode negative", model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: ptr(-1)}, model.ErrInvalidInput},
		{"slow mode above max", model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: ptr(model.MaxSlowModeSeconds + 1)}, model.ErrInvalidInput},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := m.svc.ModerateRoom(m.ctx, admin, m.room.ID, c.in); !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}

	// A crisis-held message is never restored into the room.
	crisis := m.send(m.author, other.ID, "fake fakecrisisword").Message
	if err := m.svc.ModerateRoom(m.ctx, admin, other.ID, model.Moderation{Action: model.ActionRestoreMessage, MessageID: &crisis.ID}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("crisis restore: %v", err)
	}
	if got, _ := m.store.Message(crisis.ID); got.Status != model.StatusHeld {
		t.Fatalf("crisis message status %s", got.Status)
	}

	// Restoring a held (filtered) message makes it visible and schedules its push.
	held := m.send(m.author, m.room.ID, "fake www.example.invalid").Message
	pushes := len(m.store.ActiveTimers(model.TimerPush))
	if err := m.svc.ModerateRoom(m.ctx, admin, m.room.ID, model.Moderation{Action: model.ActionRestoreMessage, MessageID: &held.ID}); err != nil {
		t.Fatal(err)
	}
	if len(m.store.ActiveTimers(model.TimerPush)) != pushes+1 {
		t.Fatal("restored message not pushed")
	}
	if a := m.lastAudit(); a.ActorRole != model.ModeratorStaff || a.Action != model.AuditRestore {
		t.Fatalf("audit %+v", a)
	}
}

func ptr[T any](v T) *T { return &v }

func TestModerateRoomStaffBanAndSlowMode(t *testing.T) {
	m := newModEnv(t)
	mod := staff("moderator")
	before := len(m.bus.Notices())
	if err := m.svc.ModerateRoom(m.ctx, mod, m.room.ID, model.Moderation{Action: model.ActionBan, MemberID: &m.author.MemberID}); err != nil {
		t.Fatal(err)
	}
	if r := m.store.Restriction(m.room.ID, m.author.MemberID); r.BannedAt == nil {
		t.Fatal("ban not stored")
	}
	if a := m.lastAudit(); a.Action != model.AuditBan || a.ActorID != mod.IdentityID || a.ObjectID != m.author.MemberID {
		t.Fatalf("ban audit %+v", a)
	}
	notices := m.bus.Notices()[before:]
	if len(notices) != 1 || notices[0].Kind != model.NoticeLeft || *notices[0].MemberID != m.author.MemberID {
		t.Fatalf("ban must end her live subscription: %+v", notices)
	}
	if _, err := m.svc.Send(m.ctx, m.author, m.room.ID, clientID(), fakeText); !errors.Is(err, model.ErrNotMember) {
		t.Fatalf("banned author posts: %v", err)
	}
	// Banning someone who is not in the room stores the ban without a notice.
	stranger := uuid.New()
	before = len(m.bus.Notices())
	if err := m.svc.ModerateRoom(m.ctx, mod, m.room.ID, model.Moderation{Action: model.ActionBan, MemberID: &stranger}); err != nil {
		t.Fatal(err)
	}
	if len(m.bus.Notices()) != before {
		t.Fatal("notice for a member who was not in the room")
	}
	if err := m.svc.ModerateRoom(m.ctx, mod, m.room.ID, model.Moderation{Action: model.ActionUnban, MemberID: &m.author.MemberID}); err != nil {
		t.Fatal(err)
	}
	if r := m.store.Restriction(m.room.ID, m.author.MemberID); r.BannedAt != nil {
		t.Fatal("unban not stored")
	}
	if m.lastAudit().Action != model.AuditUnban {
		t.Fatal("unban not audited")
	}

	if err := m.svc.ModerateRoom(m.ctx, mod, m.room.ID, model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: ptr(30)}); err != nil {
		t.Fatal(err)
	}
	if r, _ := m.store.Room(m.room.ID); r.SlowModeSeconds != 30 {
		t.Fatalf("slow mode %d", r.SlowModeSeconds)
	}
	if a := m.lastAudit(); a.Action != model.AuditSlowMode || a.ObjectType != model.AuditObjectRoom || a.ObjectID != m.room.ID {
		t.Fatalf("slow mode audit %+v", a)
	}

	// The action and its audit commit together.
	for _, method := range []string{"Audit", "SetBan", "SetSlowMode", "SetMessageStatus", "GetRoom", "IsRoomMentor"} {
		m.store.FailOn(method, errors.New("database down"))
		p := mod
		in := model.Moderation{Action: model.ActionSetSlowMode, SlowModeSeconds: ptr(5)}
		switch method {
		case "SetBan":
			in = model.Moderation{Action: model.ActionBan, MemberID: &m.reader.MemberID}
		case "SetMessageStatus":
			in = model.Moderation{Action: model.ActionHideMessage, MessageID: &m.msg.ID}
		case "IsRoomMentor":
			p = m.mentor
			in = model.Moderation{Action: model.ActionHideMessage, MessageID: &m.msg.ID}
		}
		if err := m.svc.ModerateRoom(m.ctx, p, m.room.ID, in); err == nil {
			t.Fatalf("%s failure not returned", method)
		}
		m.store.FailOn(method, nil)
	}
	if r, _ := m.store.Room(m.room.ID); r.SlowModeSeconds != 30 {
		t.Fatalf("rolled-back slow mode leaked: %d", r.SlowModeSeconds)
	}
	if r := m.store.Restriction(m.room.ID, m.reader.MemberID); r.BannedAt != nil {
		t.Fatal("rolled-back ban leaked")
	}
}

func TestAssignMentor(t *testing.T) {
	e := newEnv(t, service.Settings{})
	room := e.topicRoom("autism")
	candidate, plain := e.member(true, nil), e.member(true, nil)
	e.members.SetMentor(candidate.MemberID, true)
	direct := e.directRoom(model.OriginMatch, candidate, plain)
	admin := staff("admin")

	denied := []auth.Principal{candidate, staff("support"), {Staff: true, Roles: []string{"moderator"}}, {}}
	for _, p := range denied {
		if err := e.svc.AssignMentor(e.ctx, p, room.ID, candidate.MemberID, true); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("%+v: %v", p, err)
		}
	}
	cases := []struct {
		name   string
		room   uuid.UUID
		member uuid.UUID
		assign bool
		want   error
	}{
		{"nil member", room.ID, uuid.Nil, true, model.ErrInvalidInput},
		{"member without the mentor role", room.ID, plain.MemberID, true, model.ErrConflict},
		{"direct room", direct.ID, candidate.MemberID, true, model.ErrConflict},
		{"unknown room", uuid.New(), candidate.MemberID, true, model.ErrNotFound},
		{"assign", room.ID, candidate.MemberID, true, nil},
		{"assign again", room.ID, candidate.MemberID, true, nil},
		{"remove", room.ID, candidate.MemberID, false, nil},
		{"remove again", room.ID, candidate.MemberID, false, nil},
		// Removing needs no role check: a revoked mentor must be removable.
		{"remove a plain member", room.ID, plain.MemberID, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := e.svc.AssignMentor(e.ctx, admin, c.room, c.member, c.assign); !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
	var assigns, removes int
	for _, a := range e.store.Audit() {
		switch a.Action {
		case model.AuditMentorAssign:
			assigns++
		case model.AuditMentorRemove:
			removes++
		}
		if a.ActorID != admin.IdentityID || a.ActorRole != model.ModeratorStaff {
			t.Fatalf("audit actor %+v", a)
		}
	}
	if assigns != 2 || removes != 3 {
		t.Fatalf("audits: %d assigns, %d removes", assigns, removes)
	}

	e.members.Err = errors.New("membership down")
	if err := e.svc.AssignMentor(e.ctx, admin, room.ID, candidate.MemberID, true); err == nil || errors.Is(err, model.ErrConflict) {
		t.Fatalf("membership down: %v", err)
	}
	e.members.Err = nil
	e.store.FailOn("AssignMentor", errors.New("database down"))
	if err := e.svc.AssignMentor(e.ctx, admin, room.ID, candidate.MemberID, true); err == nil {
		t.Fatal("want store error")
	}
	e.svc.Mentors = nil
	if err := e.svc.AssignMentor(e.ctx, admin, room.ID, candidate.MemberID, true); err == nil {
		t.Fatal("assign without a mentors port")
	}
}

func TestApplyMentorRoleChangedEndsAssignments(t *testing.T) {
	e := newEnv(t, service.Settings{})
	a, b := e.topicRoom("autism"), e.topicRoom("down_syndrome")
	mentor := e.member(true, nil)
	e.join(mentor, a.ID)
	e.members.SetMentor(mentor.MemberID, true)
	admin := staff("admin")
	for _, r := range []model.Room{a, b} {
		if err := e.svc.AssignMentor(e.ctx, admin, r.ID, mentor.MemberID, true); err != nil {
			t.Fatal(err)
		}
	}
	event := uuid.New()
	revoke := model.MentorRoleChangedData{MemberID: mentor.MemberID, Mentor: false}
	if err := e.svc.ApplyMentorRoleChanged(e.ctx, event, revoke); err != nil {
		t.Fatal(err)
	}
	var system int
	for _, au := range e.store.Audit() {
		if au.ActorRole == model.ModeratorSystem {
			system++
			if au.ActorID != event || au.Action != model.AuditMentorRemove || au.Reason != model.AuditReasonMentorRevoked || au.ObjectID != mentor.MemberID {
				t.Fatalf("audit %+v", au)
			}
		}
	}
	if system != 2 {
		t.Fatalf("system audits %d, want one per room", system)
	}
	rooms, err := e.svc.StaffListRooms(e.ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rooms {
		if len(r.Mentors) != 0 {
			t.Fatalf("room %v still lists mentors", r.ID)
		}
	}
	// Even with the role back, she moderates nothing until staff assign her again.
	e.members.SetMentor(mentor.MemberID, true)
	if err := e.svc.ModerateRoom(e.ctx, mentor, a.ID, model.Moderation{Action: model.ActionMute1h, MemberID: &mentor.MemberID}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("revoked mentor moderates: %v", err)
	}
	// Replay and a second revoke write nothing.
	n := len(e.store.Audit())
	for _, ev := range []uuid.UUID{event, uuid.New()} {
		if err := e.svc.ApplyMentorRoleChanged(e.ctx, ev, revoke); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.store.Audit()) != n {
		t.Fatal("replay audited again")
	}

	for _, method := range []string{"MarkProcessed", "RemoveMemberMentorships"} {
		e.store.FailOn(method, errors.New("database down"))
		if err := e.svc.ApplyMentorRoleChanged(e.ctx, uuid.New(), revoke); err == nil {
			t.Fatalf("%s failure not returned", method)
		}
		e.store.FailOn(method, nil)
	}
	// An audit failure rolls the removal back, so the redelivery redoes it.
	if err := e.svc.AssignMentor(e.ctx, admin, a.ID, mentor.MemberID, true); err != nil {
		t.Fatal(err)
	}
	retry := uuid.New()
	e.store.FailOn("Audit", errors.New("database down"))
	if err := e.svc.ApplyMentorRoleChanged(e.ctx, retry, revoke); err == nil {
		t.Fatal("audit failure not returned")
	}
	e.store.FailOn("Audit", nil)
	if err := e.svc.ApplyMentorRoleChanged(e.ctx, retry, revoke); err != nil {
		t.Fatal(err)
	}
	if rooms, _ := e.svc.StaffListRooms(e.ctx, admin); len(rooms[0].Mentors)+len(rooms[1].Mentors) != 0 {
		t.Fatal("redelivery did not remove the mentor")
	}
}

func TestReportMessage(t *testing.T) {
	m := newModEnv(t)
	outsider := m.member(true, nil)
	held := m.send(m.author, m.room.ID, "fake www.example.invalid").Message

	cases := []struct {
		name   string
		p      auth.Principal
		msg    uuid.UUID
		reason string
		want   error
	}{
		{"staff", staff("admin"), m.msg.ID, "spam", model.ErrUnauthorized},
		{"unknown reason", m.reader, m.msg.ID, "boring", model.ErrInvalidInput},
		{"own message", m.author, m.msg.ID, "spam", model.ErrInvalidInput},
		{"unknown message", m.reader, uuid.New(), "spam", model.ErrNotFound},
		// Not in the room: she cannot learn that the message exists.
		{"outsider", outsider, m.msg.ID, "spam", model.ErrNotFound},
		{"held message of someone else", m.reader, held.ID, "spam", model.ErrNotFound},
		{"report", m.reader, m.msg.ID, "harassment", nil},
		{"report again", m.reader, m.msg.ID, "spam", nil},
		{"second reporter", m.mentor, m.msg.ID, "scam_or_fundraising", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := m.svc.ReportMessage(m.ctx, c.p, c.msg, c.reason); !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
	reported := m.store.EventsOf(model.EventMessageReported)
	if len(reported) != 2 {
		t.Fatalf("reported events %d, want one per reporter", len(reported))
	}
	if d := reported[0].Data.(model.MessageReportedData); d.MessageID != m.msg.ID || d.RoomID != m.room.ID || d.Reason != "harassment" {
		t.Fatalf("event %+v", d)
	}
	if len(m.store.Reports()) != 2 {
		t.Fatalf("reports %+v", m.store.Reports())
	}

	// A blocked author's message cannot be reported by the one who blocked her: she does not see it.
	blocker := m.member(true, nil)
	m.join(blocker, m.room.ID)
	m.members.Block(blocker.MemberID, m.author.MemberID)
	if err := m.svc.ReportMessage(m.ctx, blocker, m.msg.ID, "spam"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("blocked report: %v", err)
	}
	m.members.Err = errors.New("membership down")
	if err := m.svc.ReportMessage(m.ctx, blocker, m.msg.ID, "spam"); err == nil {
		t.Fatal("report without block information")
	}
	m.members.Err = nil
	m.store.FailOn("Publish", errors.New("database down"))
	third := m.member(true, nil)
	m.join(third, m.room.ID)
	if err := m.svc.ReportMessage(m.ctx, third, m.msg.ID, "spam"); err == nil {
		t.Fatal("publish failure not returned")
	}
	if len(m.store.Reports()) != 2 {
		t.Fatal("report kept without its event")
	}
}

func TestStaffMessageActionsDenyByDefault(t *testing.T) {
	m := newModEnv(t)
	if err := m.svc.ReportMessage(m.ctx, m.reader, m.msg.ID, "spam"); err != nil {
		t.Fatal(err)
	}
	denied := []auth.Principal{m.reader, m.mentor, staff("support"), {Staff: true, Roles: []string{"admin"}}, {}}
	for _, p := range denied {
		if err := m.svc.SetLegalHold(m.ctx, p, m.msg.ID, true); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("legal hold by %+v: %v", p, err)
		}
		if err := m.svc.CloseReports(m.ctx, p, m.msg.ID); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("close reports by %+v: %v", p, err)
		}
	}
	if len(m.store.Audit()) != 1 { // only the mentor assignment
		t.Fatalf("refused actions audited: %+v", m.store.Audit())
	}
	mod := staff("moderator")
	if err := m.svc.SetLegalHold(m.ctx, mod, uuid.New(), true); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown message: %v", err)
	}
	if err := m.svc.SetLegalHold(m.ctx, mod, m.msg.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.Message(m.msg.ID); !got.LegalHold {
		t.Fatal("legal hold not set")
	}
	if a := m.lastAudit(); a.Action != model.AuditLegalHoldOn || a.ActorID != mod.IdentityID || a.ObjectID != m.msg.ID {
		t.Fatalf("audit %+v", a)
	}
	if err := m.svc.SetLegalHold(m.ctx, mod, m.msg.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.Message(m.msg.ID); got.LegalHold || m.lastAudit().Action != model.AuditLegalHoldOff {
		t.Fatal("legal hold not released")
	}
	if err := m.svc.CloseReports(m.ctx, mod, m.msg.ID); err != nil {
		t.Fatal(err)
	}
	if r := m.store.Reports(); !r[0].Closed || m.lastAudit().Action != model.AuditReportsClose {
		t.Fatalf("reports %+v", r)
	}
	m.store.FailOn("Audit", errors.New("database down"))
	if err := m.svc.SetLegalHold(m.ctx, mod, m.msg.ID, true); err == nil {
		t.Fatal("audit failure not returned")
	}
	if got, _ := m.store.Message(m.msg.ID); got.LegalHold {
		t.Fatal("legal hold kept without its audit")
	}
}

func TestApplyModerationDecided(t *testing.T) {
	type check func(t *testing.T, m *modEnv, msg uuid.UUID)
	status := func(want string) check {
		return func(t *testing.T, m *modEnv, msg uuid.UUID) {
			if got, _ := m.store.Message(msg); got.Status != want {
				t.Fatalf("status %s, want %s", got.Status, want)
			}
		}
	}
	cases := []struct {
		name  string
		text  string
		data  func(item, msg uuid.UUID) model.ModerationDecidedData
		check []check
	}{
		{
			name: "approve a held message", text: "fake www.example.invalid",
			data: func(item, msg uuid.UUID) model.ModerationDecidedData {
				return model.ModerationDecidedData{ItemID: item, Kind: model.ModerationKindChatMessage, ObjectID: msg, Approve: true}
			},
			check: []check{status(model.StatusVisible), func(t *testing.T, m *modEnv, msg uuid.UUID) {
				if len(m.store.ActiveTimers(model.TimerPush)) != 2 { // the setup message and this one
					t.Fatal("approved message not pushed")
				}
			}},
		},
		{
			name: "reject a held message", text: "fake www.example.invalid",
			data: func(item, msg uuid.UUID) model.ModerationDecidedData {
				return model.ModerationDecidedData{ItemID: item, Kind: model.ModerationKindChatMessage, ObjectID: msg}
			},
			check: []check{status(model.StatusHidden)},
		},
		{
			name: "approve never restores a crisis message", text: "fake fakecrisisword",
			data: func(item, msg uuid.UUID) model.ModerationDecidedData {
				return model.ModerationDecidedData{ItemID: item, Kind: model.ModerationKindChatMessage, ObjectID: msg, Approve: true}
			},
			check: []check{status(model.StatusHeld)},
		},
		{
			name: "report upheld hides and closes reports", text: fakeText,
			data: func(item, msg uuid.UUID) model.ModerationDecidedData {
				return model.ModerationDecidedData{ItemID: item, Kind: model.ModerationKindReport, ObjectID: msg}
			},
			check: []check{status(model.StatusHidden), func(t *testing.T, m *modEnv, msg uuid.UUID) {
				for _, r := range m.store.Reports() {
					if !r.Closed {
						t.Fatal("report left open")
					}
				}
			}},
		},
		{
			name: "report dismissed keeps the message, closes reports", text: fakeText,
			data: func(item, msg uuid.UUID) model.ModerationDecidedData {
				return model.ModerationDecidedData{ItemID: item, Kind: model.ModerationKindReport, ObjectID: msg, Approve: true}
			},
			check: []check{status(model.StatusVisible), func(t *testing.T, m *modEnv, msg uuid.UUID) {
				if r := m.store.Reports(); len(r) != 1 || !r[0].Closed {
					t.Fatalf("reports %+v", r)
				}
			}},
		},
		{
			name: "legal hold and block author", text: fakeText,
			data: func(item, msg uuid.UUID) model.ModerationDecidedData {
				return model.ModerationDecidedData{ItemID: item, Kind: model.ModerationKindReport, ObjectID: msg,
					LegalHold: true, BlockAuthor: true, Reason: "child_safety"}
			},
			check: []check{status(model.StatusHidden), func(t *testing.T, m *modEnv, msg uuid.UUID) {
				if got, _ := m.store.Message(msg); !got.LegalHold {
					t.Fatal("legal hold not set")
				}
				calls := m.susp.Suspended()
				if len(calls) != 1 || calls[0].MemberID != m.author.MemberID || calls[0].Reason != "child_safety" {
					t.Fatalf("suspend calls %+v", calls)
				}
			}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newModEnv(t)
			msg := m.msg.ID
			if c.text != fakeText {
				msg = m.send(m.author, m.room.ID, c.text).Message.ID
			} else if err := m.svc.ReportMessage(m.ctx, m.reader, msg, "spam"); err != nil {
				t.Fatal(err)
			}
			item, event := uuid.New(), uuid.New()
			data := c.data(item, msg)
			if err := m.svc.ApplyModerationDecided(m.ctx, event, data); err != nil {
				t.Fatal(err)
			}
			for _, ch := range c.check {
				ch(t, m, msg)
			}
			for _, a := range m.store.Audit()[1:] { // [0] is the mentor assignment
				if a.ActorRole != model.ModeratorSystem || a.ActorID != item {
					t.Fatalf("audit %+v, want the system with the item id", a)
				}
			}
			// Redelivery is a no-op.
			n, s := len(m.store.Audit()), len(m.susp.Suspended())
			if err := m.svc.ApplyModerationDecided(m.ctx, event, data); err != nil {
				t.Fatal(err)
			}
			if len(m.store.Audit()) != n || len(m.susp.Suspended()) != s {
				t.Fatal("redelivery applied twice")
			}
		})
	}
}

func TestApplyModerationDecidedFailures(t *testing.T) {
	m := newModEnv(t)
	if err := m.svc.ReportMessage(m.ctx, m.reader, m.msg.ID, "spam"); err != nil {
		t.Fatal(err)
	}
	data := model.ModerationDecidedData{ItemID: uuid.New(), Kind: model.ModerationKindReport, ObjectID: m.msg.ID, BlockAuthor: true, Reason: "spam"}

	// Membership refuses the suspension: everything rolls back and the event
	// stays unprocessed, so the redelivery applies it.
	event := uuid.New()
	m.susp.Err = errors.New("membership down")
	if err := m.svc.ApplyModerationDecided(m.ctx, event, data); err == nil {
		t.Fatal("suspension failure not returned")
	}
	if got, _ := m.store.Message(m.msg.ID); got.Status != model.StatusVisible {
		t.Fatal("hide kept although the decision failed")
	}
	if r := m.store.Reports(); r[0].Closed {
		t.Fatal("reports closed although the decision failed")
	}
	m.susp.Err = nil
	if err := m.svc.ApplyModerationDecided(m.ctx, event, data); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.Message(m.msg.ID); got.Status != model.StatusHidden {
		t.Fatal("redelivery not applied")
	}

	// A message that no longer exists is acknowledged (nothing to do).
	if err := m.svc.ApplyModerationDecided(m.ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindChatMessage, ObjectID: uuid.New(),
	}); err != nil {
		t.Fatalf("missing message: %v", err)
	}
	for _, method := range []string{"MarkProcessed", "GetMessage", "SetMessageStatus", "CloseReports", "SetLegalHold", "Audit"} {
		m.store.FailOn(method, errors.New("database down"))
		err := m.svc.ApplyModerationDecided(m.ctx, uuid.New(), model.ModerationDecidedData{
			ItemID: uuid.New(), Kind: model.ModerationKindReport, ObjectID: m.msg.ID, LegalHold: true,
		})
		m.store.FailOn(method, nil)
		if method == "SetMessageStatus" {
			// Already hidden: the report path does not change the status.
			continue
		}
		if err == nil {
			t.Fatalf("%s failure not returned", method)
		}
	}
	held := m.send(m.author, m.room.ID, "fake www.example.invalid").Message
	m.store.FailOn("Schedule", errors.New("database down"))
	if err := m.svc.ApplyModerationDecided(m.ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindChatMessage, ObjectID: held.ID, Approve: true,
	}); err == nil {
		t.Fatal("push timer failure not returned")
	}
	m.store.FailOn("Schedule", nil)
	m.store.FailOn("SetMessageStatus", errors.New("database down"))
	if err := m.svc.ApplyModerationDecided(m.ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindChatMessage, ObjectID: held.ID,
	}); err == nil {
		t.Fatal("status failure not returned")
	}
}

func TestModerationSummary(t *testing.T) {
	m := newModEnv(t)
	text, err := m.svc.ModerationSummary(m.ctx, m.msg.ID)
	if err != nil || text != fakeText {
		t.Fatalf("summary %q %v", text, err)
	}
	// Hidden messages keep their text for review.
	if err := m.svc.ModerateRoom(m.ctx, staff("admin"), m.room.ID, model.Moderation{Action: model.ActionHideMessage, MessageID: &m.msg.ID}); err != nil {
		t.Fatal(err)
	}
	if text, err := m.svc.ModerationSummary(m.ctx, m.msg.ID); err != nil || text != fakeText {
		t.Fatalf("hidden summary %q %v", text, err)
	}
	if _, err := m.svc.ModerationSummary(m.ctx, uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	m.sealer.Shred(m.author.MemberID)
	if _, err := m.svc.ModerationSummary(m.ctx, m.msg.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("shredded author: %v", err)
	}
	m.svc.Sealer = nil
	if _, err := m.svc.ModerationSummary(m.ctx, m.msg.ID); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("no sealer: %v", err)
	}
}
