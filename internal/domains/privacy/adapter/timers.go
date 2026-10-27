package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/privacy/model"
	"birge/internal/domains/privacy/service"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timer due rows to privacy service methods.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerDeletionDue: h.DeletionDue,
	}
}

// DeletionDue handles privacy.DeletionDue: mark the request completed and shred the key.
func (h *TimerHandlers) DeletionDue(ctx context.Context, _ pgx.Tx, due timers.Due) error {
	return h.Service.HandleDeletionDue(ctx, due.SubjectID)
}
