// Package adapter implements the knowledge ports on PostgreSQL (schema
// knowledge, sqlc in ../store) and the platform hooks it needs: outbox
// events, review-due and expire timers, the audit log, the timer handlers
// and DEMO seeding into empty tables.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/port"
	knowledgestore "birge/internal/domains/knowledge/store"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// PostgresStore implements port.Store on PostgreSQL.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// InTx runs fn in a transaction (READ COMMITTED, the PostgreSQL default).
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(Bind(pgxTx))
	})
}

// Bind returns a port.Tx working inside an existing transaction, such as the
// one the timer worker passes to a handler.
func Bind(tx pgx.Tx) port.Tx {
	return &postgresTx{tx: tx, q: knowledgestore.New(tx), platform: platformstore.New(tx)}
}

type postgresTx struct {
	tx       pgx.Tx
	q        *knowledgestore.Queries
	platform *platformstore.Queries
}

func notFound(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// InsertTopic inserts a topic; a duplicate code is model.ErrConflict.
func (t *postgresTx) InsertTopic(ctx context.Context, topic model.Topic) (model.Topic, error) {
	row, err := t.q.InsertTopic(ctx, knowledgestore.InsertTopicParams{
		ID: topic.ID, Kind: topic.Kind, Code: topic.Code,
		TitleKk: topic.TitleKK, TitleRu: topic.TitleRU, TitleEn: topic.TitleEN,
	})
	if isUniqueViolation(err) {
		return model.Topic{}, model.ErrConflict
	}
	if err != nil {
		return model.Topic{}, fmt.Errorf("insert topic: %w", err)
	}
	return mapTopic(row), nil
}

// GetTopic implements port.Tx.
func (t *postgresTx) GetTopic(ctx context.Context, id uuid.UUID) (model.Topic, error) {
	row, err := t.q.GetTopic(ctx, id)
	if err != nil {
		return model.Topic{}, notFound(err, "get topic")
	}
	return mapTopic(row), nil
}

// ListAllTopics implements port.Tx.
func (t *postgresTx) ListAllTopics(ctx context.Context) ([]model.Topic, error) {
	rows, err := t.q.ListAllTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("list topics: %w", err)
	}
	out := make([]model.Topic, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapTopic(r))
	}
	return out, nil
}

// ListMemberTopics implements port.Tx.
func (t *postgresTx) ListMemberTopics(ctx context.Context, kind, lang string) ([]model.Topic, error) {
	rows, err := t.q.ListMemberTopics(ctx, knowledgestore.ListMemberTopicsParams{Kind: kind, Lang: lang})
	if err != nil {
		return nil, fmt.Errorf("list member topics: %w", err)
	}
	out := make([]model.Topic, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapTopic(r))
	}
	return out, nil
}

// InsertArticle implements port.Tx.
func (t *postgresTx) InsertArticle(ctx context.Context, a model.Article) (model.Article, error) {
	row, err := t.q.InsertArticle(ctx, knowledgestore.InsertArticleParams{
		ID: a.ID, TopicID: a.TopicID, Kind: a.Kind, CreatedAt: a.CreatedAt,
	})
	if err != nil {
		return model.Article{}, fmt.Errorf("insert article: %w", err)
	}
	return mapArticle(row), nil
}

// GetArticle implements port.Tx.
func (t *postgresTx) GetArticle(ctx context.Context, id uuid.UUID) (model.Article, error) {
	row, err := t.q.GetArticle(ctx, id)
	if err != nil {
		return model.Article{}, notFound(err, "get article")
	}
	return mapArticle(row), nil
}

// ListTopicArticleSummaries implements port.Tx.
func (t *postgresTx) ListTopicArticleSummaries(ctx context.Context, topicID uuid.UUID, lang string, now time.Time) ([]model.ArticleSummary, error) {
	rows, err := t.q.ListTopicArticleSummaries(ctx, knowledgestore.ListTopicArticleSummariesParams{
		TopicID: topicID, Lang: lang,
	})
	if err != nil {
		return nil, fmt.Errorf("list topic articles: %w", err)
	}
	out := make([]model.ArticleSummary, 0, len(rows))
	for _, r := range rows {
		expires := time.Time{}
		if r.ExpiresAt != nil {
			expires = *r.ExpiresAt
		}
		out = append(out, model.ArticleSummary{
			ID: r.ID, Title: r.Title, Status: model.AvailabilityOf(r.Kind, expires, now),
		})
	}
	return out, nil
}

// GetPublishedArticle implements port.Tx.
func (t *postgresTx) GetPublishedArticle(ctx context.Context, articleID uuid.UUID, lang string, now time.Time) (model.MemberArticle, error) {
	row, err := t.q.GetPublishedArticle(ctx, knowledgestore.GetPublishedArticleParams{
		ArticleID: articleID, Lang: lang,
	})
	if err != nil {
		return model.MemberArticle{}, notFound(err, "get published article")
	}
	if row.ReviewedAt == nil || row.ExpiresAt == nil {
		return model.MemberArticle{}, fmt.Errorf("get published article: missing review fields")
	}
	return model.MemberArticle{
		ID: row.ArticleID, TopicID: row.TopicID, Kind: row.Kind, Lang: row.Lang,
		Title: row.Title, Body: row.Body, SourceURL: row.SourceUrl,
		ReviewedAt: *row.ReviewedAt, ExpiresAt: *row.ExpiresAt,
		Status: model.AvailabilityOf(row.Kind, *row.ExpiresAt, now),
	}, nil
}

// ListPublishedFragments implements port.Tx.
func (t *postgresTx) ListPublishedFragments(ctx context.Context, kind, lang string, now time.Time) ([]model.Fragment, error) {
	n := now
	rows, err := t.q.ListPublishedFragments(ctx, knowledgestore.ListPublishedFragmentsParams{
		Kind: kind, Lang: lang, Now: &n,
	})
	if err != nil {
		return nil, fmt.Errorf("list fragments: %w", err)
	}
	out := make([]model.Fragment, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.Fragment{
			ArticleID: r.ArticleID, VersionID: r.VersionID, Title: r.Title,
			SourceURL: r.SourceUrl, Body: r.Body,
		})
	}
	return out, nil
}

// InsertVersion implements port.Tx.
func (t *postgresTx) InsertVersion(ctx context.Context, v model.Version) (model.Version, bool, error) {
	row, err := t.q.InsertVersion(ctx, knowledgestore.InsertVersionParams{
		ID: v.ID, ArticleID: v.ArticleID, Lang: v.Lang, Status: v.Status,
		Title: v.Title, Body: v.Body, SourceUrl: v.SourceURL, AuthorID: v.AuthorID,
		NeedsSync: v.NeedsSync, IdempotencyKey: v.IdempotencyKey,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Version{}, false, nil
	}
	if isUniqueViolation(err) {
		return model.Version{}, false, model.ErrConflict
	}
	if err != nil {
		return model.Version{}, false, fmt.Errorf("insert version: %w", err)
	}
	return mapVersion(row), true, nil
}

// GetVersionByIdempotencyKey implements port.Tx.
func (t *postgresTx) GetVersionByIdempotencyKey(ctx context.Context, authorID, key uuid.UUID) (model.Version, error) {
	k := key
	row, err := t.q.GetVersionByIdempotencyKey(ctx, knowledgestore.GetVersionByIdempotencyKeyParams{
		AuthorID: authorID, IdempotencyKey: &k,
	})
	if err != nil {
		return model.Version{}, notFound(err, "get version by key")
	}
	return mapVersion(row), nil
}

// GetVersion implements port.Tx.
func (t *postgresTx) GetVersion(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Version, error) {
	var row knowledgestore.KnowledgeVersion
	var err error
	if forUpdate {
		row, err = t.q.GetVersionForUpdate(ctx, id)
	} else {
		row, err = t.q.GetVersion(ctx, id)
	}
	if err != nil {
		return model.Version{}, notFound(err, "get version")
	}
	return mapVersion(row), nil
}

// GetVersionWithArticle implements port.Tx.
func (t *postgresTx) GetVersionWithArticle(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Version, error) {
	if forUpdate {
		row, err := t.q.GetVersionWithArticleForUpdate(ctx, id)
		if err != nil {
			return model.Version{}, notFound(err, "get version")
		}
		return mapVersionWith(row.ID, row.ArticleID, row.Lang, row.Status, row.Title, row.Body, row.SourceUrl,
			row.AuthorID, row.ReviewerID, row.ReviewedAt, row.ExpiresAt, row.RejectReason, row.NeedsSync,
			row.IdempotencyKey, row.CreatedAt, row.UpdatedAt, row.TopicID, row.Kind), nil
	}
	row, err := t.q.GetVersionWithArticle(ctx, id)
	if err != nil {
		return model.Version{}, notFound(err, "get version")
	}
	return mapVersionWith(row.ID, row.ArticleID, row.Lang, row.Status, row.Title, row.Body, row.SourceUrl,
		row.AuthorID, row.ReviewerID, row.ReviewedAt, row.ExpiresAt, row.RejectReason, row.NeedsSync,
		row.IdempotencyKey, row.CreatedAt, row.UpdatedAt, row.TopicID, row.Kind), nil
}

// UpdateDraft implements port.Tx.
func (t *postgresTx) UpdateDraft(ctx context.Context, id, authorID uuid.UUID, c model.Content, at time.Time) (model.Version, error) {
	row, err := t.q.UpdateDraft(ctx, knowledgestore.UpdateDraftParams{
		Title: c.Title, Body: c.Body, SourceUrl: c.SourceURL, UpdatedAt: at, ID: id, AuthorID: authorID,
	})
	if err != nil {
		return model.Version{}, notFound(err, "update draft")
	}
	return mapVersion(row), nil
}

// SubmitVersion implements port.Tx.
func (t *postgresTx) SubmitVersion(ctx context.Context, id, authorID uuid.UUID, at time.Time) (model.Version, error) {
	row, err := t.q.SubmitVersion(ctx, knowledgestore.SubmitVersionParams{
		UpdatedAt: at, ID: id, AuthorID: authorID,
	})
	if err != nil {
		return model.Version{}, notFound(err, "submit version")
	}
	return mapVersion(row), nil
}

// RejectVersion implements port.Tx.
func (t *postgresTx) RejectVersion(ctx context.Context, id uuid.UUID, reason string, at time.Time) (model.Version, error) {
	row, err := t.q.RejectVersion(ctx, knowledgestore.RejectVersionParams{
		RejectReason: &reason, UpdatedAt: at, ID: id,
	})
	if err != nil {
		return model.Version{}, notFound(err, "reject version")
	}
	return mapVersion(row), nil
}

// PublishVersion implements port.Tx.
func (t *postgresTx) PublishVersion(ctx context.Context, id, reviewerID uuid.UUID, reviewedAt, expiresAt time.Time) (model.Version, error) {
	row, err := t.q.PublishVersion(ctx, knowledgestore.PublishVersionParams{
		ReviewerID: &reviewerID, ReviewedAt: &reviewedAt, ExpiresAt: &expiresAt,
		UpdatedAt: reviewedAt, ID: id,
	})
	if err != nil {
		return model.Version{}, notFound(err, "publish version")
	}
	return mapVersion(row), nil
}

// RetirePublished implements port.Tx.
func (t *postgresTx) RetirePublished(ctx context.Context, articleID uuid.UUID, lang string, exceptID uuid.UUID, at time.Time) (model.Version, bool, error) {
	row, err := t.q.RetirePublished(ctx, knowledgestore.RetirePublishedParams{
		UpdatedAt: at, ArticleID: articleID, Lang: lang, ExceptID: exceptID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Version{}, false, nil
	}
	if err != nil {
		return model.Version{}, false, fmt.Errorf("retire published: %w", err)
	}
	return mapVersion(row), true, nil
}

// MarkOtherLanguagesNeedsSync implements port.Tx.
func (t *postgresTx) MarkOtherLanguagesNeedsSync(ctx context.Context, articleID uuid.UUID, lang string, at time.Time) error {
	if err := t.q.MarkOtherLanguagesNeedsSync(ctx, knowledgestore.MarkOtherLanguagesNeedsSyncParams{
		UpdatedAt: at, ArticleID: articleID, Lang: lang,
	}); err != nil {
		return fmt.Errorf("mark needs_sync: %w", err)
	}
	return nil
}

// ListStaffVersions implements port.Tx.
func (t *postgresTx) ListStaffVersions(ctx context.Context, f model.VersionFilter) ([]model.Version, error) {
	var status *string
	if f.Status != "" {
		status = &f.Status
	}
	rows, err := t.q.ListStaffVersions(ctx, knowledgestore.ListStaffVersionsParams{
		Status: status, CursorUpdatedAt: f.CursorUpdatedAt, CursorID: f.CursorID, PageLimit: f.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list staff versions: %w", err)
	}
	out := make([]model.Version, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapVersionWith(r.ID, r.ArticleID, r.Lang, r.Status, r.Title, r.Body, r.SourceUrl,
			r.AuthorID, r.ReviewerID, r.ReviewedAt, r.ExpiresAt, r.RejectReason, r.NeedsSync,
			r.IdempotencyKey, r.CreatedAt, r.UpdatedAt, r.TopicID, r.Kind))
	}
	return out, nil
}

// Publish enqueues e in this transaction's outbox.
func (t *postgresTx) Publish(ctx context.Context, now time.Time, e model.Event) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{Type: e.Type, Version: e.Version, Data: e.Data})
	return err
}

// ScheduleReviewDue schedules knowledge.ReviewDue (dedup key = version id).
func (t *postgresTx) ScheduleReviewDue(ctx context.Context, versionID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind: model.TimerReviewDue, SubjectID: versionID, DedupKey: versionID.String(), DueAt: dueAt, Payload: map[string]string{},
	})
}

// ScheduleExpire schedules knowledge.Expire (dedup key = version id).
func (t *postgresTx) ScheduleExpire(ctx context.Context, versionID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind: model.TimerExpire, SubjectID: versionID, DedupKey: versionID.String(), DueAt: dueAt, Payload: map[string]string{},
	})
}

// CancelVersionTimers cancels pending ReviewDue and Expire timers for the version.
func (t *postgresTx) CancelVersionTimers(ctx context.Context, versionID uuid.UUID, at time.Time) error {
	if _, err := timers.Cancel(ctx, t.platform, at, model.TimerReviewDue, versionID); err != nil {
		return fmt.Errorf("cancel review due: %w", err)
	}
	if _, err := timers.Cancel(ctx, t.platform, at, model.TimerExpire, versionID); err != nil {
		return fmt.Errorf("cancel expire: %w", err)
	}
	return nil
}

func mapTopic(r knowledgestore.KnowledgeTopic) model.Topic {
	return model.Topic{
		ID: r.ID, Kind: r.Kind, Code: r.Code, TitleKK: r.TitleKk, TitleRU: r.TitleRu, TitleEN: r.TitleEn, CreatedAt: r.CreatedAt,
	}
}

func mapArticle(r knowledgestore.KnowledgeArticle) model.Article {
	return model.Article{ID: r.ID, TopicID: r.TopicID, Kind: r.Kind, CreatedAt: r.CreatedAt}
}

func mapVersion(r knowledgestore.KnowledgeVersion) model.Version {
	return mapVersionWith(r.ID, r.ArticleID, r.Lang, r.Status, r.Title, r.Body, r.SourceUrl,
		r.AuthorID, r.ReviewerID, r.ReviewedAt, r.ExpiresAt, r.RejectReason, r.NeedsSync,
		r.IdempotencyKey, r.CreatedAt, r.UpdatedAt, uuid.Nil, "")
}

func mapVersionWith(
	id, articleID uuid.UUID, lang, status, title, body string, sourceURL *string,
	authorID uuid.UUID, reviewerID *uuid.UUID, reviewedAt, expiresAt *time.Time, rejectReason *string,
	needsSync bool, idempotencyKey *uuid.UUID, createdAt, updatedAt time.Time, topicID uuid.UUID, kind string,
) model.Version {
	return model.Version{
		ID: id, ArticleID: articleID, TopicID: topicID, Kind: kind, Lang: lang, Status: status,
		Title: title, Body: body, SourceURL: sourceURL, AuthorID: authorID, ReviewerID: reviewerID,
		ReviewedAt: reviewedAt, ExpiresAt: expiresAt, RejectReason: rejectReason, NeedsSync: needsSync,
		IdempotencyKey: idempotencyKey, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}
