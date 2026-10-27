package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/service"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timers to circle service methods.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerMatchRun:      h.MatchRun,
		model.TimerRequestExpire: h.RequestExpire,
		model.TimerAskMet:        h.AskMet,
	}
}

// MatchRun handles circle.MatchRun on the worker's transaction.
func (h *TimerHandlers) MatchRun(ctx context.Context, tx pgx.Tx, _ timers.Due) error {
	return h.Service.HandleMatchRun(ctx, Bind(tx))
}

// RequestExpire handles circle.RequestExpire (subject = request id).
func (h *TimerHandlers) RequestExpire(ctx context.Context, tx pgx.Tx, due timers.Due) error {
	return h.Service.HandleRequestExpire(ctx, Bind(tx), due.SubjectID)
}

// AskMet handles circle.AskMet (subject = match id).
func (h *TimerHandlers) AskMet(ctx context.Context, tx pgx.Tx, due timers.Due) error {
	return h.Service.HandleAskMet(ctx, Bind(tx), due.SubjectID)
}
