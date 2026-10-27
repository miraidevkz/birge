package outbox

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/platform/store"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		e       Event
		wantErr bool
	}{
		{"valid", Event{Type: "safety.EscalationOpened", Version: 1}, false},
		{"no context", Event{Type: "EscalationOpened", Version: 1}, true},
		{"lowercase name", Event{Type: "safety.escalationOpened", Version: 1}, true},
		{"zero version", Event{Type: "safety.EscalationOpened", Version: 0}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validate(tt.e); (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnvelope(t *testing.T) {
	id := uuid.MustParse("01890a5d-ac96-774b-bcce-b302099a8057")
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.FixedZone("ALMT", 5*3600))
	body, err := envelope(store.ClaimUnpublishedEventsRow{
		ID: id, EventType: "safety.EscalationOpened", EventVersion: 1,
		Payload: []byte(`{"escalation_id":"x"}`), TraceParent: &tp, OccurredAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != id.String() || got["type"] != "safety.EscalationOpened" || got["trace_parent"] != tp {
		t.Errorf("unexpected envelope: %s", body)
	}
	if got["occurred_at"] != "2026-10-27T07:00:00Z" {
		t.Errorf("occurred_at = %v, want UTC", got["occurred_at"])
	}
	if Subject("safety.EscalationOpened") != "birge.events.safety.EscalationOpened" {
		t.Error("unexpected subject")
	}
}
