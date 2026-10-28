package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/service"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timers to the analytics service. The handler
// runs in the worker's transaction, so the sweep commits together with the
// timer being marked done: a crash repeats it whole, never half-way.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{model.TimerRetention: h.Retention}
}

// Retention handles the daily analytics.Retention sweep.
func (h *TimerHandlers) Retention(ctx context.Context, tx pgx.Tx, _ timers.Due) error {
	return h.Service.HandleRetention(ctx, Bind(tx))
}
