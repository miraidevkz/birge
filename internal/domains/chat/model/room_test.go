package model_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
)

func TestRoomMembershipHelpers(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	low, high := model.OrderPair(a, b)
	direct := model.Room{Kind: model.KindDirect, MemberLow: &low, MemberHigh: &high}
	if direct.Group() || !direct.DirectMember(a) || !direct.DirectMember(b) || direct.DirectMember(c) {
		t.Fatal("direct room membership")
	}
	if direct.Other(a) != b || direct.Other(b) != a {
		t.Fatal("other member of a direct room")
	}
	topic := model.Room{Kind: model.KindTopic}
	district := model.Room{Kind: model.KindDistrict}
	if !topic.Group() || !district.Group() || topic.DirectMember(a) || topic.Other(a) != uuid.Nil {
		t.Fatal("group room helpers")
	}
	// A group room never lets a member in through the direct-member check,
	// even if its pair columns were set by mistake.
	odd := model.Room{Kind: model.KindTopic, MemberLow: &low, MemberHigh: &high}
	if odd.DirectMember(a) {
		t.Fatal("group room treated as direct")
	}

	left := time.Now()
	if !(model.RoomMember{}).Active() || (model.RoomMember{LeftAt: &left}).Active() {
		t.Fatal("active membership")
	}
}

func TestEventsCarryIDsOnly(t *testing.T) {
	msg := model.Message{ID: uuid.New(), RoomID: uuid.New(), HoldReason: model.HoldLink, Text: []byte("sealed")}
	held := model.MessageHeld(msg)
	if d := held.Data.(model.MessageHeldData); held.Type != model.EventMessageHeld || d.MessageID != msg.ID || d.RoomID != msg.RoomID || d.Reason != model.HoldLink {
		t.Fatalf("held %+v", held)
	}
	rep := model.MessageReported(msg.ID, msg.RoomID, "spam")
	if d := rep.Data.(model.MessageReportedData); rep.Type != model.EventMessageReported || d.MessageID != msg.ID || d.Reason != "spam" {
		t.Fatalf("reported %+v", rep)
	}
	if got := model.RoomSubject(msg.RoomID); got != "birge.chat.room."+msg.RoomID.String() {
		t.Fatalf("subject %s", got)
	}
	if model.Retention(model.KindDirect, time.Hour, time.Minute) != time.Minute ||
		model.Retention(model.KindTopic, time.Hour, time.Minute) != time.Hour ||
		model.Retention(model.KindDistrict, time.Hour, time.Minute) != time.Hour {
		t.Fatal("retention per room kind")
	}
}
