package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/service"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timers to sharing service methods.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerProcessPhoto: h.ProcessPhoto,
		model.TimerAutoClose:    h.AutoClose,
	}
}

// ProcessPhoto handles sharing.ProcessPhoto (subject = listing id). Object I/O
// runs outside the worker transaction; Bind(tx) is unused for the photo path.
func (h *TimerHandlers) ProcessPhoto(ctx context.Context, _ pgx.Tx, due timers.Due) error {
	return h.Service.HandleProcessPhoto(ctx, due.SubjectID)
}

// AutoClose handles sharing.AutoClose (subject = listing id).
func (h *TimerHandlers) AutoClose(ctx context.Context, tx pgx.Tx, due timers.Due) error {
	return h.Service.HandleAutoClose(ctx, Bind(tx), due.SubjectID)
}
