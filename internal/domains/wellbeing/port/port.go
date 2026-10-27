// Package port defines the interfaces the wellbeing service needs.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/wellbeing/model"
)

// Sealer encrypts check-in answers with the member's data key (ADR-0009).
// Associated data must be wellbeing.checkins.answers|<checkin id>. Fakes in tests only.
type Sealer interface {
	Seal(ctx context.Context, memberID uuid.UUID, plaintext, associatedData []byte) ([]byte, error)
}

// CrisisOpener opens a human escalation when a check-in flags crisis (implemented by safety).
type CrisisOpener interface {
	OpenEscalation(ctx context.Context, memberID uuid.UUID, source string) (uuid.UUID, error)
}

// Store is the persistence port for wellbeing tables and platform hooks
// (outbox events and the reminder timer).
type Store interface {
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction.
type Tx interface {
	UpsertSchedule(ctx context.Context, s model.Schedule) (model.Schedule, error)
	GetSchedule(ctx context.Context, memberID uuid.UUID) (model.Schedule, error)

	// InsertCheckin inserts c; inserted is false when the member/idempotency key already exists.
	InsertCheckin(ctx context.Context, c model.Checkin) (created model.Checkin, inserted bool, err error)
	GetCheckinByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.Checkin, error)
	ListCheckinHistory(ctx context.Context, memberID uuid.UUID, since time.Time, limit int32) ([]model.Checkin, error)

	ListPracticesByLang(ctx context.Context, lang string) ([]model.Practice, error)

	PublishCheckinCompleted(ctx context.Context, now time.Time, data model.CheckinCompletedData) error
	PublishDeliveryRequested(ctx context.Context, now time.Time, data model.DeliveryRequestedData) error

	// ScheduleReminder enqueues wellbeing.Reminder (dedup key = member id); scheduled is false on dedup hit.
	ScheduleReminder(ctx context.Context, memberID uuid.UUID, dueAt time.Time) (scheduled bool, err error)
	// CancelReminder cancels active wellbeing.Reminder timers for the member.
	CancelReminder(ctx context.Context, memberID uuid.UUID, at time.Time) (cancelled int64, err error)
}
