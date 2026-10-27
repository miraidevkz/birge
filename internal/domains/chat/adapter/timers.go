package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timers to chat service methods. Each runs
// in the worker's transaction, so its writes commit together with the timer
// being marked done: a crash fires it again, never half-way.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerCrisisEscalation: h.CrisisEscalation,
		model.TimerPush:             h.Push,
		model.TimerRetention:        h.Retention,
	}
}

// CrisisEscalation handles chat.CrisisEscalation (subject = message id).
func (h *TimerHandlers) CrisisEscalation(ctx context.Context, tx pgx.Tx, due timers.Due) error {
	return h.Service.HandleCrisisEscalation(ctx, Bind(tx), due.SubjectID)
}

// Push handles chat.PushFanout (subject = message id).
func (h *TimerHandlers) Push(ctx context.Context, tx pgx.Tx, due timers.Due) error {
	return h.Service.HandlePush(ctx, Bind(tx), due.SubjectID)
}

// Retention handles the daily chat.RetentionPurge.
func (h *TimerHandlers) Retention(ctx context.Context, tx pgx.Tx, _ timers.Due) error {
	return h.Service.HandleRetention(ctx, Bind(tx))
}
