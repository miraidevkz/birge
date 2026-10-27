package audit

import (
	"testing"

	"github.com/google/uuid"
)

func TestValidate(t *testing.T) {
	actor := uuid.MustParse("01890a5d-ac96-774b-bcce-b302099a8057")
	valid := Entry{
		ActorID:    actor,
		ActorRole:  "psychologist",
		Action:     "escalation.take",
		ObjectType: "safety.escalation",
		ObjectID:   actor,
	}
	tests := []struct {
		name    string
		mut     func(*Entry)
		wantErr bool
	}{
		{"valid", nil, false},
		{"valid with reason code", func(e *Entry) { e.Reason = "duty.handoff" }, false},
		{"nil actor", func(e *Entry) { e.ActorID = uuid.Nil }, true},
		{"empty role", func(e *Entry) { e.ActorRole = "" }, true},
		{"empty action", func(e *Entry) { e.Action = "" }, true},
		{"action uppercase", func(e *Entry) { e.Action = "Escalation.take" }, true},
		{"action free text", func(e *Entry) { e.Action = "took the escalation" }, true},
		{"empty object type", func(e *Entry) { e.ObjectType = "" }, true},
		{"object type free text", func(e *Entry) { e.ObjectType = "an escalation" }, true},
		{"reason free text", func(e *Entry) { e.Reason = "member asked for help" }, true},
		{"nil object id ok", func(e *Entry) { e.ObjectID = uuid.Nil }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := valid
			if tt.mut != nil {
				tt.mut(&e)
			}
			if err := validate(e); (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
