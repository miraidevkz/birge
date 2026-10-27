package adapter

import (
	"context"

	"github.com/jackc/pgx/v5"

	"birge/internal/domains/wellbeing/model"
	"birge/internal/domains/wellbeing/port"
	"birge/internal/domains/wellbeing/service"
	wellbeingstore "birge/internal/domains/wellbeing/store"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// TimerHandlers adapts platform timer due rows to wellbeing service methods.
type TimerHandlers struct {
	Service *service.Service
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *TimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{
		model.TimerReminder: h.Reminder,
	}
}

// TxFrom binds a wellbeing port.Tx to an existing database transaction.
func TxFrom(tx pgx.Tx) port.Tx {
	return &postgresTx{
		wellbeing: wellbeingstore.New(tx),
		platform:  platformstore.New(tx),
	}
}

// Reminder handles wellbeing.Reminder on the worker's transaction so the active
// dedup row can be cancelled before the next weekly occurrence is scheduled.
func (h *TimerHandlers) Reminder(ctx context.Context, tx pgx.Tx, due timers.Due) error {
	return h.Service.HandleReminderTx(ctx, TxFrom(tx), due.SubjectID, due.DueAt)
}
