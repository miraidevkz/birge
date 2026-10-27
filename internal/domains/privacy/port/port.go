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
	AppendConsent(ctx context.Context, r model.ConsentRecord) (model.ConsentRecord, error)
	GetLatestConsent(ctx context.Context, memberID uuid.UUID, consentType string) (model.ConsentRecord, error)
	ListLatestConsents(ctx context.Context, memberID uuid.UUID) ([]model.ConsentRecord, error)

	InsertDeletionRequest(ctx context.Context, r model.DeletionRequest) (created model.DeletionRequest, inserted bool, err error)
	GetDeletionRequest(ctx context.Context, memberID uuid.UUID) (model.DeletionRequest, error)
	MarkDeletionCompleted(ctx context.Context, memberID uuid.UUID, at time.Time) (model.DeletionRequest, bool, error)

	PublishConsentChanged(ctx context.Context, now time.Time, data model.ConsentChangedData) error
	PublishDataDeletionRequested(ctx context.Context, now time.Time, data model.DataDeletionRequestedData) error
	ScheduleDeletionDue(ctx context.Context, memberID uuid.UUID, dueAt time.Time) (scheduled bool, err error)
}
