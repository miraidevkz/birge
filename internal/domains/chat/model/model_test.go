package model_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
)

func TestHoldFor(t *testing.T) {
	cases := []struct {
		text, want string
	}{
		{"hello, see you at the playground", ""},
		{"visit https://example.com now", model.HoldLink},
		{"www.example.org has it", model.HoldLink},
		{"write me at t.me/example_handle", model.HoldLink},
		{"go to example.kz please", model.HoldLink},
		{"call +7 (700) 000-00-00", model.HoldPhone},
		{"номер 87000000000", model.HoldPhone},
		{"7000000000", model.HoldPhone},
		{"my son is 7 and my daughter 12", ""},
		{"room 12 34 56", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := model.HoldFor(c.text); got != c.want {
			t.Errorf("HoldFor(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestView(t *testing.T) {
	author, other := uuid.New(), uuid.New()
	base := model.Opened{Message: model.Message{ID: uuid.New(), AuthorID: author, Status: model.StatusVisible}, PlainText: "fake text", AuthorName: "Fakename"}

	if v, ok := model.View(base, other); !ok || v.Text == nil || *v.Text != "fake text" || v.Mine {
		t.Fatalf("visible: %+v %v", v, ok)
	}
	held := base
	held.Status = model.StatusHeld
	if _, ok := model.View(held, other); ok {
		t.Fatal("held message visible to another member")
	}
	if v, ok := model.View(held, author); !ok || v.Status != model.StatusHeld || !v.Mine || v.Text == nil {
		t.Fatalf("held for author: %+v %v", v, ok)
	}
	hidden := base
	hidden.Status = model.StatusHidden
	if v, ok := model.View(hidden, other); !ok || v.Text != nil || v.Status != model.StatusHidden {
		t.Fatalf("hidden: %+v", v)
	}
	deleted := base
	deleted.Deleted, deleted.PlainText = true, ""
	if v, ok := model.View(deleted, other); !ok || v.Text != nil || v.Status != model.StatusDeleted {
		t.Fatalf("deleted: %+v", v)
	}
}

func TestAllowed(t *testing.T) {
	for _, a := range []string{model.ActionHideMessage, model.ActionRestoreMessage, model.ActionMute1h, model.ActionBan, model.ActionUnban, model.ActionSetSlowMode} {
		if !model.Allowed(model.ModeratorStaff, a) {
			t.Errorf("staff refused %s", a)
		}
	}
	for a, want := range map[string]bool{
		model.ActionHideMessage: true, model.ActionRestoreMessage: true, model.ActionMute1h: true,
		model.ActionBan: false, model.ActionUnban: false, model.ActionSetSlowMode: false,
	} {
		if got := model.Allowed(model.ModeratorMentor, a); got != want {
			t.Errorf("mentor %s = %v, want %v", a, got, want)
		}
	}
	if model.Allowed("member", model.ActionHideMessage) || model.Allowed(model.ModeratorStaff, "delete_everything") {
		t.Fatal("unknown moderator or action allowed")
	}
	if !model.IsStaffModerator([]string{"psychologist", "moderator"}) || model.IsStaffModerator([]string{"lawyer"}) {
		t.Fatal("staff roles")
	}
}

func TestValidate(t *testing.T) {
	if model.ValidateClientID(uuid.New()) == nil {
		t.Error("v4 accepted as client id")
	}
	if err := model.ValidateClientID(uuid.Must(uuid.NewV7())); err != nil {
		t.Error(err)
	}
	if model.ValidateText("   ") == nil || model.ValidateText(string(make([]rune, model.MaxTextRunes+1))) == nil || model.ValidateText("\xff") == nil {
		t.Error("invalid text accepted")
	}
	if err := model.ValidateText("fake ok"); err != nil {
		t.Error(err)
	}
}

func TestOrderPairAndPush(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	l1, h1 := model.OrderPair(a, b)
	l2, h2 := model.OrderPair(b, a)
	if l1 != l2 || h1 != h2 || l1 == h1 {
		t.Fatal("pair order not canonical")
	}
	m, room, member := uuid.New(), uuid.New(), uuid.New()
	e1 := model.NewMessagePush(m, room, member).Data.(model.DeliveryRequestedData)
	e2 := model.NewMessagePush(m, room, member).Data.(model.DeliveryRequestedData)
	if e1.RequestID != e2.RequestID || e1.Template != model.TemplateNewMessage || e1.Class != model.ClassNormal || e1.Params["room_id"] != room.String() || len(e1.Params) != 1 {
		t.Fatalf("push: %+v", e1)
	}
}

func TestNextRetentionRun(t *testing.T) {
	at := 3 * time.Hour
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	if got := model.NextRetentionRun(now, at); !got.Equal(time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)) {
		t.Fatal(got)
	}
	now = time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)
	if got := model.NextRetentionRun(now, at); !got.Equal(time.Date(2026, 10, 28, 3, 0, 0, 0, time.UTC)) {
		t.Fatal(got)
	}
}

func TestRestrictionBlocks(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Minute), now.Add(-time.Minute)
	if (model.Restriction{}).Blocks(now) || (model.Restriction{MutedUntil: &earlier}).Blocks(now) {
		t.Fatal("no restriction blocks")
	}
	if !(model.Restriction{MutedUntil: &later}).Blocks(now) || !(model.Restriction{BannedAt: &earlier}).Blocks(now) {
		t.Fatal("restriction does not block")
	}
}
