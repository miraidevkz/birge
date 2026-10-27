// Package adapter implements the safety ports on PostgreSQL (schema safety,
// sqlc in ../store) and the platform hooks it needs: outbox events, the
// escalation backup timer, the audit log and the timer handler.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/safety/model"
	"birge/internal/domains/safety/port"
	safetystore "birge/internal/domains/safety/store"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// PostgresStore implements port.Store on PostgreSQL.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// InTx runs fn in a transaction (READ COMMITTED, the PostgreSQL default; the
// partial unique index on active escalations makes concurrent opens safe).
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(Bind(pgxTx))
	})
}

// Bind returns a port.Tx working inside an existing transaction, such as the
// one the timer worker passes to a handler.
func Bind(tx pgx.Tx) port.Tx {
	return &postgresTx{tx: tx, safety: safetystore.New(tx), platform: platformstore.New(tx)}
}

type postgresTx struct {
	tx       pgx.Tx
	safety   *safetystore.Queries
	platform *platformstore.Queries
}

// InsertEscalation inserts an open escalation; inserted is false when the member has an active one.
func (t *postgresTx) InsertEscalation(ctx context.Context, e model.Escalation) (model.Escalation, bool, error) {
	row, err := t.safety.InsertEscalation(ctx, safetystore.InsertEscalationParams{
		MemberID:   e.MemberID,
		Source:     e.Source,
		DistrictID: e.DistrictID,
		OpenedAt:   e.OpenedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Escalation{}, false, nil
	}
	if err != nil {
		return model.Escalation{}, false, fmt.Errorf("insert escalation: %w", err)
	}
	return mapEscalation(row), true, nil
}

// GetActiveEscalation returns the member's open or taken escalation, or model.ErrNotFound.
func (t *postgresTx) GetActiveEscalation(ctx context.Context, memberID uuid.UUID) (model.Escalation, error) {
	row, err := t.safety.GetActiveEscalationByMember(ctx, memberID)
	return escalationResult(row, err, "get active escalation")
}

// GetEscalation returns the escalation, optionally locked, or model.ErrNotFound.
func (t *postgresTx) GetEscalation(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Escalation, error) {
	var row safetystore.SafetyEscalation
	var err error
	if forUpdate {
		row, err = t.safety.GetEscalationForUpdate(ctx, id)
	} else {
		row, err = t.safety.GetEscalation(ctx, id)
	}
	return escalationResult(row, err, "get escalation")
}

// TakeEscalation moves an open escalation to taken; updated is false when it was not open.
func (t *postgresTx) TakeEscalation(ctx context.Context, id, staffID uuid.UUID, at time.Time) (model.Escalation, bool, error) {
	row, err := t.safety.TakeEscalation(ctx, safetystore.TakeEscalationParams{TakenBy: &staffID, TakenAt: &at, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Escalation{}, false, nil
	}
	if err != nil {
		return model.Escalation{}, false, fmt.Errorf("take escalation: %w", err)
	}
	return mapEscalation(row), true, nil
}

// CloseEscalation closes an escalation taken by staffID; updated is false otherwise.
func (t *postgresTx) CloseEscalation(ctx context.Context, id, staffID uuid.UUID, outcome string, sealedNote []byte, at time.Time) (model.Escalation, bool, error) {
	row, err := t.safety.CloseEscalation(ctx, safetystore.CloseEscalationParams{
		Outcome: &outcome, Note: sealedNote, ClosedAt: &at, ID: id, TakenBy: &staffID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Escalation{}, false, nil
	}
	if err != nil {
		return model.Escalation{}, false, fmt.Errorf("close escalation: %w", err)
	}
	return mapEscalation(row), true, nil
}

// ListEscalations returns one page of the staff queue, newest first.
func (t *postgresTx) ListEscalations(ctx context.Context, f model.EscalationFilter) ([]model.Escalation, error) {
	var status *string
	if f.Status != "" {
		status = &f.Status
	}
	rows, err := t.safety.ListEscalations(ctx, safetystore.ListEscalationsParams{
		Status: status, CursorOpenedAt: f.CursorOpenedAt, CursorID: f.CursorID, PageLimit: f.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list escalations: %w", err)
	}
	out := make([]model.Escalation, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapEscalation(r))
	}
	return out, nil
}

// InsertDutyShift inserts a shift; inserted is false when the id exists.
func (t *postgresTx) InsertDutyShift(ctx context.Context, s model.DutyShift) (model.DutyShift, bool, error) {
	row, err := t.safety.InsertDutyShift(ctx, safetystore.InsertDutyShiftParams{
		ID: s.ID, StaffID: s.StaffID, Role: s.Role, StartsAt: s.StartsAt, EndsAt: s.EndsAt,
		Backup: s.Backup, Phone: s.Phone, CreatedAt: s.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.DutyShift{}, false, nil
	}
	if err != nil {
		return model.DutyShift{}, false, fmt.Errorf("insert duty shift: %w", err)
	}
	return mapShift(row), true, nil
}

// GetDutyShift returns the shift, or model.ErrNotFound.
func (t *postgresTx) GetDutyShift(ctx context.Context, id uuid.UUID) (model.DutyShift, error) {
	row, err := t.safety.GetDutyShift(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.DutyShift{}, model.ErrNotFound
	}
	if err != nil {
		return model.DutyShift{}, fmt.Errorf("get duty shift: %w", err)
	}
	return mapShift(row), nil
}

// ListDutyShifts returns active shifts overlapping [from, to).
func (t *postgresTx) ListDutyShifts(ctx context.Context, from, to time.Time, limit int32) ([]model.DutyShift, error) {
	rows, err := t.safety.ListDutyShifts(ctx, safetystore.ListDutyShiftsParams{PeriodEnd: to, PeriodStart: from, PageLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list duty shifts: %w", err)
	}
	return mapShifts(rows), nil
}

// ListCoveringShifts returns active shifts of role/backup that cover at.
func (t *postgresTx) ListCoveringShifts(ctx context.Context, role string, backup bool, at time.Time) ([]model.DutyShift, error) {
	rows, err := t.safety.ListCoveringShifts(ctx, safetystore.ListCoveringShiftsParams{Role: role, Backup: backup, At: at})
	if err != nil {
		return nil, fmt.Errorf("list covering shifts: %w", err)
	}
	return mapShifts(rows), nil
}

// CancelDutyShift cancels a shift that has not started; updated is false otherwise.
func (t *postgresTx) CancelDutyShift(ctx context.Context, id uuid.UUID, at time.Time) (model.DutyShift, bool, error) {
	row, err := t.safety.CancelDutyShift(ctx, safetystore.CancelDutyShiftParams{CancelledAt: &at, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.DutyShift{}, false, nil
	}
	if err != nil {
		return model.DutyShift{}, false, fmt.Errorf("cancel duty shift: %w", err)
	}
	return mapShift(row), true, nil
}

// Publish enqueues e in this transaction's outbox.
func (t *postgresTx) Publish(ctx context.Context, now time.Time, e model.Event) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{Type: e.Type, Version: e.Version, Data: e.Data})
	return err
}

// ScheduleBackup schedules safety.EscalationBackup (dedup key = escalation id).
func (t *postgresTx) ScheduleBackup(ctx context.Context, escalationID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind:      model.TimerEscalationBackup,
		SubjectID: escalationID,
		DedupKey:  escalationID.String(),
		DueAt:     dueAt,
		Payload:   map[string]string{},
	})
}

// CancelBackup cancels the pending safety.EscalationBackup timer of the escalation.
func (t *postgresTx) CancelBackup(ctx context.Context, escalationID uuid.UUID, at time.Time) (int64, error) {
	n, err := timers.Cancel(ctx, t.platform, at, model.TimerEscalationBackup, escalationID)
	if err != nil {
		return 0, fmt.Errorf("cancel backup timer: %w", err)
	}
	return n, nil
}

func escalationResult(row safetystore.SafetyEscalation, err error, op string) (model.Escalation, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Escalation{}, model.ErrNotFound
	}
	if err != nil {
		return model.Escalation{}, fmt.Errorf("%s: %w", op, err)
	}
	return mapEscalation(row), nil
}

func mapEscalation(r safetystore.SafetyEscalation) model.Escalation {
	return model.Escalation{
		ID: r.ID, MemberID: r.MemberID, Source: r.Source, Status: r.Status, DistrictID: r.DistrictID,
		OpenedAt: r.OpenedAt, TakenBy: r.TakenBy, TakenAt: r.TakenAt, Outcome: r.Outcome, Note: r.Note,
		ClosedAt: r.ClosedAt,
	}
}

func mapShift(r safetystore.SafetyDutyShift) model.DutyShift {
	return model.DutyShift{
		ID: r.ID, StaffID: r.StaffID, Role: r.Role, StartsAt: r.StartsAt, EndsAt: r.EndsAt,
		Backup: r.Backup, Phone: r.Phone, CreatedAt: r.CreatedAt, CancelledAt: r.CancelledAt,
	}
}

func mapShifts(rows []safetystore.SafetyDutyShift) []model.DutyShift {
	out := make([]model.DutyShift, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapShift(r))
	}
	return out
}
