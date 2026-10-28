// Package adapter implements wellbeing ports on PostgreSQL and platform hooks
// (outbox events and the weekly reminder timer).
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/wellbeing/model"
	"birge/internal/domains/wellbeing/port"
	wellbeingstore "birge/internal/domains/wellbeing/store"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// PostgresStore implements port.Store on PostgreSQL (wellbeing schema + platform hooks).
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// InTx runs fn in a transaction.
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(&postgresTx{
			wellbeing: wellbeingstore.New(pgxTx),
			platform:  platformstore.New(pgxTx),
		})
	})
}

type postgresTx struct {
	wellbeing *wellbeingstore.Queries
	platform  *platformstore.Queries
}

// UpsertSchedule inserts or replaces the member's reminder schedule.
func (t *postgresTx) UpsertSchedule(ctx context.Context, s model.Schedule) (model.Schedule, error) {
	row, err := t.wellbeing.UpsertSchedule(ctx, wellbeingstore.UpsertScheduleParams{
		MemberID:  s.MemberID,
		Weekday:   int32(s.Weekday), //nolint:gosec // G115: weekday validated 1..7
		Hour:      int32(s.Hour),    //nolint:gosec // G115: hour validated 0..23
		Enabled:   s.Enabled,
		UpdatedAt: s.UpdatedAt,
	})
	if err != nil {
		return model.Schedule{}, fmt.Errorf("upsert schedule: %w", err)
	}
	return mapSchedule(row), nil
}

// GetSchedule returns the member's schedule or model.ErrNotFound.
func (t *postgresTx) GetSchedule(ctx context.Context, memberID uuid.UUID) (model.Schedule, error) {
	row, err := t.wellbeing.GetSchedule(ctx, memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Schedule{}, model.ErrNotFound
	}
	if err != nil {
		return model.Schedule{}, err
	}
	return mapSchedule(row), nil
}

// InsertCheckin inserts c; inserted is false when the member/idempotency key already exists.
func (t *postgresTx) InsertCheckin(ctx context.Context, c model.Checkin) (model.Checkin, bool, error) {
	row, err := t.wellbeing.InsertCheckin(ctx, wellbeingstore.InsertCheckinParams{
		ID:                   c.ID,
		MemberID:             c.MemberID,
		QuestionnaireVersion: c.QuestionnaireVersion,
		Answers:              c.SealedAnswers,
		Phq4:                 int32(c.PHQ4), //nolint:gosec // G115: PHQ-4 scored 0..12
		Pss4:                 int32(c.PSS4), //nolint:gosec // G115: PSS-4 scored 0..16
		Flag:                 c.Flag,
		IdempotencyKey:       c.IdempotencyKey,
		CompletedAt:          c.CompletedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Checkin{}, false, nil
	}
	if err != nil {
		return model.Checkin{}, false, fmt.Errorf("insert checkin: %w", err)
	}
	return mapCheckin(row), true, nil
}

// GetCheckinByIdempotencyKey returns the check-in for member/key or model.ErrNotFound.
func (t *postgresTx) GetCheckinByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.Checkin, error) {
	row, err := t.wellbeing.GetCheckinByIdempotencyKey(ctx, wellbeingstore.GetCheckinByIdempotencyKeyParams{
		MemberID:       memberID,
		IdempotencyKey: key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Checkin{}, model.ErrNotFound
	}
	if err != nil {
		return model.Checkin{}, err
	}
	return mapCheckin(row), nil
}

// ListCheckinHistory returns check-ins completed at or after since, oldest first.
func (t *postgresTx) ListCheckinHistory(ctx context.Context, memberID uuid.UUID, since time.Time, limit int32) ([]model.Checkin, error) {
	rows, err := t.wellbeing.ListCheckinHistory(ctx, wellbeingstore.ListCheckinHistoryParams{
		MemberID: memberID,
		Since:    since,
		Lim:      limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.Checkin, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapCheckin(row))
	}
	return out, nil
}

// ListCohortCheckins returns the scores of members whose first check-in is in
// [firstFrom, firstTo), completed before until, ordered by member then time.
func (t *postgresTx) ListCohortCheckins(ctx context.Context, firstFrom, firstTo, until time.Time) ([]model.CohortCheckin, error) {
	rows, err := t.wellbeing.ListCohortCheckins(ctx, wellbeingstore.ListCohortCheckinsParams{
		FirstFrom: firstFrom,
		FirstTo:   firstTo,
		Until:     until,
	})
	if err != nil {
		return nil, fmt.Errorf("list cohort checkins: %w", err)
	}
	out := make([]model.CohortCheckin, 0, len(rows))
	for _, row := range rows {
		out = append(out, model.CohortCheckin{MemberID: row.MemberID, PSS4: int(row.Pss4), CompletedAt: row.CompletedAt})
	}
	return out, nil
}

// ListPracticesByLang returns practices for lang (URL filled by the service).
func (t *postgresTx) ListPracticesByLang(ctx context.Context, lang string) ([]model.Practice, error) {
	rows, err := t.wellbeing.ListPracticesByLang(ctx, lang)
	if err != nil {
		return nil, err
	}
	out := make([]model.Practice, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapPractice(row))
	}
	return out, nil
}

// PublishCheckinCompleted enqueues wellbeing.CheckinCompleted in the outbox of this transaction.
func (t *postgresTx) PublishCheckinCompleted(ctx context.Context, now time.Time, data model.CheckinCompletedData) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{
		Type:    model.EventCheckinCompleted,
		Version: model.EventCheckinCompletedVersion,
		Data:    data,
	})
	return err
}

// PublishDeliveryRequested enqueues notify.DeliveryRequested in this transaction's outbox.
func (t *postgresTx) PublishDeliveryRequested(ctx context.Context, now time.Time, data model.DeliveryRequestedData) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{
		Type:    model.EventDeliveryRequested,
		Version: model.EventDeliveryRequestedVersion,
		Data:    data,
	})
	return err
}

// ScheduleReminder enqueues wellbeing.Reminder (dedup key = member id); scheduled is false on dedup hit.
func (t *postgresTx) ScheduleReminder(ctx context.Context, memberID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind:      model.TimerReminder,
		SubjectID: memberID,
		DedupKey:  memberID.String(),
		DueAt:     dueAt,
		Payload:   map[string]string{},
	})
}

// CancelReminder cancels active wellbeing.Reminder timers for the member.
func (t *postgresTx) CancelReminder(ctx context.Context, memberID uuid.UUID, at time.Time) (int64, error) {
	return timers.Cancel(ctx, t.platform, at, model.TimerReminder, memberID)
}

func mapSchedule(row wellbeingstore.WellbeingSchedule) model.Schedule {
	return model.Schedule{
		MemberID:  row.MemberID,
		Weekday:   int(row.Weekday),
		Hour:      int(row.Hour),
		Enabled:   row.Enabled,
		UpdatedAt: row.UpdatedAt,
	}
}

func mapCheckin(row wellbeingstore.WellbeingCheckin) model.Checkin {
	return model.Checkin{
		ID:                   row.ID,
		MemberID:             row.MemberID,
		QuestionnaireVersion: row.QuestionnaireVersion,
		SealedAnswers:        row.Answers,
		PHQ4:                 int(row.Phq4),
		PSS4:                 int(row.Pss4),
		Flag:                 row.Flag,
		IdempotencyKey:       row.IdempotencyKey,
		CompletedAt:          row.CompletedAt,
	}
}

func mapPractice(row wellbeingstore.WellbeingPractice) model.Practice {
	return model.Practice{
		ID:              row.ID,
		Lang:            row.Lang,
		TitleKey:        row.TitleKey,
		DurationSeconds: int(row.DurationSeconds),
		ObjectKey:       row.ObjectKey,
	}
}

var _ port.Store = (*PostgresStore)(nil)
var _ port.Tx = (*postgresTx)(nil)
