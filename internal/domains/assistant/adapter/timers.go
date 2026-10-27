package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/assistant/model"
	"birge/internal/domains/assistant/service"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timer due rows to assistant service methods.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerRetentionPurge: h.RetentionPurge,
	}
}

// RetentionPurge runs on the worker's transaction so the claimed timer can be
// cancelled before the next one is scheduled under the same dedup key.
func (h *TimerHandlers) RetentionPurge(ctx context.Context, tx pgx.Tx, _ timers.Due) error {
	return h.Service.HandleRetentionPurgeTx(ctx, TxFrom(tx))
}
