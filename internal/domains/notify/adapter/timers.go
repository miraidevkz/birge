package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/service"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timer due rows to notify service methods.
//
// Delivery is at-most-once per successful handler run (status check before
// send). At-least-once edge: if the provider accepts the message but updating
// the delivery row fails (or the worker transaction rolls back after a
// successful side effect in a separate store transaction), a timer retry may
// contact the provider again.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerPush:        h.Push,
		model.TimerExternalSMS: h.ExternalSMS,
		model.TimerCriticalSMS: h.CriticalSMS,
	}
}

// Push handles notify.Push (due now; dedup key = notification id).
func (h *TimerHandlers) Push(ctx context.Context, _ pgx.Tx, due timers.Due) error {
	return h.Service.HandlePush(ctx, due.SubjectID)
}

// ExternalSMS handles notify.ExternalSMS for phone-only recipients.
func (h *TimerHandlers) ExternalSMS(ctx context.Context, _ pgx.Tx, due timers.Due) error {
	return h.Service.HandleExternalSMS(ctx, due.SubjectID)
}

// CriticalSMS handles notify.CriticalSMS after the acknowledgement timeout.
func (h *TimerHandlers) CriticalSMS(ctx context.Context, _ pgx.Tx, due timers.Due) error {
	return h.Service.HandleCriticalSMS(ctx, due.SubjectID)
}
