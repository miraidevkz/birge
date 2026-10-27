package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/service"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timers to knowledge service methods.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerReviewDue: h.ReviewDue,
		model.TimerExpire:    h.Expire,
	}
}

// ReviewDue handles knowledge.ReviewDue (subject = version id).
func (h *TimerHandlers) ReviewDue(ctx context.Context, tx pgx.Tx, due timers.Due) error {
	return h.Service.HandleReviewDue(ctx, Bind(tx), due.SubjectID)
}

// Expire handles knowledge.Expire (subject = version id).
func (h *TimerHandlers) Expire(ctx context.Context, tx pgx.Tx, due timers.Due) error {
	return h.Service.HandleExpire(ctx, Bind(tx), due.SubjectID)
}
