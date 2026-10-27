package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"birge/internal/domains/safety/model"
	"birge/internal/domains/safety/safetytest"
	"birge/internal/platform/crypto"
)

// After account deletion her key is shredded: a note cannot be sealed for
// her any more. The escalation still closes (staff accountability), without
// the note, and the drop is logged by escalation ID.
func TestCloseDropsNoteWhenMemberKeyShredded(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.open(ctx, t, uuid.New())
	psy := staff(model.RoleModerator)
	if _, err := e.svc.TakeEscalation(ctx, psy, id); err != nil {
		t.Fatal(err)
	}
	e.svc.Sealer = &safetytest.FakeSealer{Err: crypto.ErrShredded}
	note := fakeNote
	got, err := e.svc.CloseEscalation(ctx, psy, id, model.OutcomeContacted, &note)
	if err != nil {
		t.Fatalf("close after shred: %v", err)
	}
	if got.Status != model.StatusClosed || got.Note != nil {
		t.Fatalf("closed escalation = %+v, want closed without a note", got)
	}
	out := e.logs.String()
	if !strings.Contains(out, "EscalationNoteDroppedMemberDeleted") || !strings.Contains(out, id.String()) {
		t.Fatalf("note drop not logged with the escalation id: %s", out)
	}
}
