// Package port defines the interfaces the knowledge service needs: its store
// (topics, articles, versions, outbox, timers, audit). Adapters live in
// ../adapter; tests use the fakes in knowledgetest.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/model"
)

// Auditor appends to platform.audit_log in the current transaction. Entries
// carry IDs and fixed codes only.
type Auditor interface {
	Audit(ctx context.Context, e model.AuditEntry) error
}

// Store is the persistence port for knowledge tables and platform hooks.
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

	InsertTopic(ctx context.Context, t model.Topic) (model.Topic, error)
	GetTopic(ctx context.Context, id uuid.UUID) (model.Topic, error)
	ListAllTopics(ctx context.Context) ([]model.Topic, error)
	ListMemberTopics(ctx context.Context, kind, lang string) ([]model.Topic, error)

	InsertArticle(ctx context.Context, a model.Article) (model.Article, error)
	GetArticle(ctx context.Context, id uuid.UUID) (model.Article, error)
	ListTopicArticleSummaries(ctx context.Context, topicID uuid.UUID, lang string, now time.Time) ([]model.ArticleSummary, error)
	GetPublishedArticle(ctx context.Context, articleID uuid.UUID, lang string, now time.Time) (model.MemberArticle, error)
	ListPublishedFragments(ctx context.Context, kind, lang string, now time.Time) ([]model.Fragment, error)

	// InsertVersion inserts a draft; inserted is false on a repeated idempotency key.
	InsertVersion(ctx context.Context, v model.Version) (created model.Version, inserted bool, err error)
	GetVersionByIdempotencyKey(ctx context.Context, authorID, key uuid.UUID) (model.Version, error)
	GetVersion(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Version, error)
	GetVersionWithArticle(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Version, error)
	UpdateDraft(ctx context.Context, id, authorID uuid.UUID, c model.Content, at time.Time) (model.Version, error)
	SubmitVersion(ctx context.Context, id, authorID uuid.UUID, at time.Time) (model.Version, error)
	RejectVersion(ctx context.Context, id uuid.UUID, reason string, at time.Time) (model.Version, error)
	PublishVersion(ctx context.Context, id, reviewerID uuid.UUID, reviewedAt, expiresAt time.Time) (model.Version, error)
	// RetirePublished retires the previous published version of (article, lang)
	// other than exceptID; retired is false when there was none.
	RetirePublished(ctx context.Context, articleID uuid.UUID, lang string, exceptID uuid.UUID, at time.Time) (retired model.Version, ok bool, err error)
	MarkOtherLanguagesNeedsSync(ctx context.Context, articleID uuid.UUID, lang string, at time.Time) error
	ListStaffVersions(ctx context.Context, f model.VersionFilter) ([]model.Version, error)
	// MarkReviewDue sets review_due_at when the ReviewDue timer fires (idempotent).
	MarkReviewDue(ctx context.Context, versionID uuid.UUID, at time.Time) (model.Version, error)
	// ListReviewDue returns published versions with review_due_at set, oldest expiry first.
	ListReviewDue(ctx context.Context, f model.ReviewDueFilter) ([]model.Version, error)
	// ListExportVersions returns published versions for repository export (ADR-0022).
	ListExportVersions(ctx context.Context, now time.Time) ([]model.ExportArticle, error)

	// Publish enqueues e in this transaction's outbox (ADR-0007).
	Publish(ctx context.Context, now time.Time, e model.Event) error
	// ScheduleReviewDue schedules knowledge.ReviewDue (dedup key = version id).
	ScheduleReviewDue(ctx context.Context, versionID uuid.UUID, dueAt time.Time) (bool, error)
	// ScheduleExpire schedules knowledge.Expire (dedup key = version id).
	ScheduleExpire(ctx context.Context, versionID uuid.UUID, dueAt time.Time) (bool, error)
	// CancelVersionTimers cancels pending ReviewDue and Expire timers for the version.
	CancelVersionTimers(ctx context.Context, versionID uuid.UUID, at time.Time) error
}
