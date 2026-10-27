// Package port defines the interfaces the moderation service needs: its store
// (queue items, outbox, audit, processed_events) and one ContentSource per
// kind, through which the owning context shows the content under review.
// Adapters for other contexts are wired later; tests use the fakes in
// moderationtest.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/moderation/model"
)

// ContentSource returns the summary of one object of the owning context for
// the staff queue. Contract for implementations:
//   - no contact data: no phones, emails, addresses or specialist contacts;
//     a verification summary is the first name and the district only;
//   - model.ErrNotFound when the object no longer exists or its content was
//     purged (the item is then shown with an empty summary);
//   - any other error is an outage: the queue page fails rather than show an
//     item a moderator cannot see.
//
// Moderation never stores, logs or publishes what it returns.
type ContentSource interface {
	Summary(ctx context.Context, objectID uuid.UUID) (model.Summary, error)
}

// Store is the persistence port for the moderation schema and platform hooks.
type Store interface {
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction.
type Tx interface {
	// MarkProcessed records eventID for consumer; newly is false for a duplicate.
	MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (newly bool, err error)
	// InsertItem creates the open item of an object; created is false when
	// the object already has an open item.
	InsertItem(ctx context.Context, it model.Item) (created bool, err error)
	// UpsertReportItem creates the open report item of a message or bumps
	// the report counter of the open one, returning the item ID and count.
	UpsertReportItem(ctx context.Context, it model.Item) (id uuid.UUID, count int, err error)
	// GetItemForUpdate loads an item and locks its row.
	GetItemForUpdate(ctx context.Context, id uuid.UUID) (model.Item, error)
	// DecideItem marks an open item decided; updated is false when it was not open.
	DecideItem(ctx context.Context, id uuid.UUID, by uuid.UUID, at time.Time, d model.Decision) (updated bool, err error)
	// ListOpenItems returns open items oldest first after the cursor.
	ListOpenItems(ctx context.Context, f ListFilter) ([]model.Item, error)

	Publish(ctx context.Context, now time.Time, e model.Event) error
	Audit(ctx context.Context, e AuditEntry) error
}

// ListFilter selects a page of open items.
type ListFilter struct {
	Kind            model.Kind // empty: all kinds
	CursorCreatedAt time.Time  // zero: from the start
	CursorID        uuid.UUID
	Limit           int32
}

// AuditEntry is one platform.audit_log row written in the decision transaction.
type AuditEntry struct {
	ActorID    uuid.UUID
	ActorRole  string
	Action     string
	ObjectType string
	ObjectID   uuid.UUID
	Reason     string
}
