// Package port defines the interfaces the privacy service needs.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/privacy/model"
)

// KeyShredder destroys the member's data encryption key (ADR-0009).
// Implemented later by the platform crypto package; tests use a fake.
type KeyShredder interface {
	Shred(ctx context.Context, memberID uuid.UUID) error
}

// Store is the persistence port for privacy tables and platform hooks
// (outbox events and the deletion-due timer).
type Store interface {
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction.
type Tx interface {
	// AppendConsent inserts one append-only consent_history row and returns it with its id.
	AppendConsent(ctx context.Context, r model.ConsentRecord) (model.ConsentRecord, error)
	// GetLatestConsent returns the newest history row for memberID/type or model.ErrNotFound.
	GetLatestConsent(ctx context.Context, memberID uuid.UUID, consentType string) (model.ConsentRecord, error)
	// ListLatestConsents returns the newest history row per consent type for the member.
	ListLatestConsents(ctx context.Context, memberID uuid.UUID) ([]model.ConsentRecord, error)

	// InsertDeletionRequest creates the member's deletion request; inserted is false when one exists.
	InsertDeletionRequest(ctx context.Context, r model.DeletionRequest) (created model.DeletionRequest, inserted bool, err error)
	// GetDeletionRequest returns the member's deletion request or model.ErrNotFound.
	GetDeletionRequest(ctx context.Context, memberID uuid.UUID) (model.DeletionRequest, error)
	// MarkDeletionCompleted sets completed_at once; updated is false when absent or already completed.
	MarkDeletionCompleted(ctx context.Context, memberID uuid.UUID, at time.Time) (model.DeletionRequest, bool, error)
	// EraseConsents deletes the member's consent history (the only delete
	// of that table, run after the key is shredded) and records the audit
	// entry privacy.member_deleted once.
	EraseConsents(ctx context.Context, memberID uuid.UUID) error

	// PublishConsentChanged enqueues privacy.ConsentChanged in the outbox of this transaction.
	PublishConsentChanged(ctx context.Context, now time.Time, data model.ConsentChangedData) error
	// PublishDataDeletionRequested enqueues privacy.DataDeletionRequested in this transaction's outbox.
	PublishDataDeletionRequested(ctx context.Context, now time.Time, data model.DataDeletionRequestedData) error
	// ScheduleDeletionDue enqueues privacy.DeletionDue (dedup key = member id); scheduled is false on dedup hit.
	ScheduleDeletionDue(ctx context.Context, memberID uuid.UUID, dueAt time.Time) (scheduled bool, err error)
}
