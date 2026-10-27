// Package port defines the interfaces the safety service needs: its store
// (escalations, duty shifts, outbox, timers, audit), field sealing and the
// member directory.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/safety/model"
)

// Sealer encrypts and decrypts one subject's fields with that subject's data
// key (platform crypto, ADR-0009). Escalation notes are sealed under the
// member's key, so account deletion makes them unreadable; on-duty phones
// are sealed under the staff member's key. associatedData binds a ciphertext
// to its column and row (crypto.AssociatedData).
type Sealer interface {
	Seal(ctx context.Context, subjectID uuid.UUID, plaintext, associatedData []byte) ([]byte, error)
	Open(ctx context.Context, subjectID uuid.UUID, ciphertext, associatedData []byte) ([]byte, error)
}

// MemberDirectory reads member details owned by the membership context.
// Implemented later by an adapter over membership's service; tests use fakes.
// Results are never logged, stored or put into events.
type MemberDirectory interface {
	// Contact returns the member's display name, language and phone, or
	// model.ErrNotFound.
	Contact(ctx context.Context, memberID uuid.UUID) (model.Contact, error)
	// District returns the member's district for routing, or nil when the
	// member has none.
	District(ctx context.Context, memberID uuid.UUID) (*uuid.UUID, error)
}

// Auditor appends to platform.audit_log in the current transaction. Entries
// carry IDs and fixed codes only.
type Auditor interface {
	Audit(ctx context.Context, e model.AuditEntry) error
}

// Store is the persistence port for safety tables and platform hooks.
type Store interface {
	// InTx runs fn in one database transaction: committed when fn returns
	// nil, rolled back otherwise.
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction. Every write a use case makes
// (rows, outbox events, timers, audit) goes through the same Tx so they
// commit or roll back together.
type Tx interface {
	Auditor

	// InsertEscalation inserts an open escalation; inserted is false when the
	// member already has an open or taken one (partial unique index).
	InsertEscalation(ctx context.Context, e model.Escalation) (created model.Escalation, inserted bool, err error)
	// GetActiveEscalation returns the member's open or taken escalation, or model.ErrNotFound.
	GetActiveEscalation(ctx context.Context, memberID uuid.UUID) (model.Escalation, error)
	// GetEscalation returns the escalation, or model.ErrNotFound. With
	// forUpdate the row stays locked until the transaction ends.
	GetEscalation(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Escalation, error)
	// TakeEscalation moves an open escalation to taken; updated is false when
	// it was not open.
	TakeEscalation(ctx context.Context, id, staffID uuid.UUID, at time.Time) (e model.Escalation, updated bool, err error)
	// CloseEscalation closes an escalation taken by staffID; updated is false
	// when it was not taken by staffID.
	CloseEscalation(ctx context.Context, id, staffID uuid.UUID, outcome string, sealedNote []byte, at time.Time) (e model.Escalation, updated bool, err error)
	// ListEscalations returns one page of the staff queue, newest first.
	ListEscalations(ctx context.Context, f model.EscalationFilter) ([]model.Escalation, error)

	// InsertDutyShift inserts a shift whose phone is already sealed; inserted
	// is false when a shift with this id exists.
	InsertDutyShift(ctx context.Context, s model.DutyShift) (created model.DutyShift, inserted bool, err error)
	// GetDutyShift returns the shift, or model.ErrNotFound.
	GetDutyShift(ctx context.Context, id uuid.UUID) (model.DutyShift, error)
	// ListDutyShifts returns active shifts that overlap [from, to), by start time.
	ListDutyShifts(ctx context.Context, from, to time.Time, limit int32) ([]model.DutyShift, error)
	// ListCoveringShifts returns active shifts of role and backup flag that cover at.
	ListCoveringShifts(ctx context.Context, role string, backup bool, at time.Time) ([]model.DutyShift, error)
	// CancelDutyShift cancels a shift that has not started at at; updated is
	// false when it is cancelled already or has started.
	CancelDutyShift(ctx context.Context, id uuid.UUID, at time.Time) (s model.DutyShift, updated bool, err error)

	// Publish enqueues e in this transaction's outbox (ADR-0007).
	Publish(ctx context.Context, now time.Time, e model.Event) error
	// ScheduleBackup schedules safety.EscalationBackup for the escalation
	// (dedup key = escalation id); scheduled is false on a dedup hit.
	ScheduleBackup(ctx context.Context, escalationID uuid.UUID, dueAt time.Time) (scheduled bool, err error)
	// CancelBackup cancels the pending backup timer; returns how many were cancelled.
	CancelBackup(ctx context.Context, escalationID uuid.UUID, at time.Time) (int64, error)
}
