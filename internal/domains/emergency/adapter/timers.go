package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/service"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timers to emergency service methods.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerSosAutoClose: h.SosAutoClose,
	}
}

// SosAutoClose handles emergency.SosAutoClose (subject = SOS id). It runs in
// the worker's transaction, so the close and its event commit together with
// the timer being marked done: a crash fires it again, never half-way.
func (h *TimerHandlers) SosAutoClose(ctx context.Context, tx pgx.Tx, due timers.Due) error {
	return h.Service.HandleAutoClose(ctx, Bind(tx), due.SubjectID)
}
