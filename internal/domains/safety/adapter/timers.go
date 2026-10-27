package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/safety/model"
	"birge/internal/domains/safety/service"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timers to safety service methods.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerEscalationBackup: h.EscalationBackup,
	}
}

// EscalationBackup handles safety.EscalationBackup (subject = escalation id).
// It runs in the worker's transaction, so the events it publishes commit
// together with the timer being marked done: a crash fires it again, never
// half-way.
func (h *TimerHandlers) EscalationBackup(ctx context.Context, tx pgx.Tx, due timers.Due) error {
	return h.Service.HandleEscalationBackup(ctx, Bind(tx), due.SubjectID)
}
