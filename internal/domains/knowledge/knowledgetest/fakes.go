// Package knowledgetest holds in-memory fakes of the knowledge ports for unit
// tests.
package knowledgetest

import (
	"context"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/port"
)

// ScheduledTimer is a recorded platform timer.
type ScheduledTimer struct {
	Kind      string
	SubjectID uuid.UUID
	DueAt     time.Time
	Cancelled bool
}

type state struct {
	topics   map[uuid.UUID]model.Topic
	articles map[uuid.UUID]model.Article
	versions map[uuid.UUID]model.Version
	byKey    map[string]uuid.UUID // author|key → version id
	events   []model.Event
	timers   []ScheduledTimer
	audit    []model.AuditEntry
}

func (s *state) clone() *state {
	versions := maps.Clone(s.versions)
	return &state{
		topics: maps.Clone(s.topics), articles: maps.Clone(s.articles),
		versions: versions, byKey: maps.Clone(s.byKey),
		events: slices.Clone(s.events), timers: slices.Clone(s.timers),
		audit: slices.Clone(s.audit),
	}
}

// MemoryStore is an in-memory port.Store with atomic transactions.
type MemoryStore struct {
	mu sync.Mutex
	st *state
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{st: &state{
		topics: map[uuid.UUID]model.Topic{}, articles: map[uuid.UUID]model.Article{},
		versions: map[uuid.UUID]model.Version{}, byKey: map[string]uuid.UUID{},
	}}
}

// InTx implements port.Store.
func (m *MemoryStore) InTx(_ context.Context, fn func(tx port.Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	work := m.st.clone()
	if err := fn(&memoryTx{st: work}); err != nil {
		return err
	}
	m.st = work
	return nil
}

// Tx returns a port.Tx and a commit function for timer handlers.
func (m *MemoryStore) Tx() (port.Tx, func()) {
	m.mu.Lock()
	work := m.st.clone()
	return &memoryTx{st: work}, func() {
		m.st = work
		m.mu.Unlock()
	}
}

// EventsOfType returns committed events of type t.
func (m *MemoryStore) EventsOfType(t string) []model.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Event
	for _, e := range m.st.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// Timers returns committed timers (including cancelled).
func (m *MemoryStore) Timers() []ScheduledTimer {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.st.timers)
}

// ActiveTimersOf returns non-cancelled timers of kind.
func (m *MemoryStore) ActiveTimersOf(kind string) []ScheduledTimer {
	var out []ScheduledTimer
	for _, t := range m.Timers() {
		if t.Kind == kind && !t.Cancelled {
			out = append(out, t)
		}
	}
	return out
}

// Audit returns committed audit entries.
func (m *MemoryStore) Audit() []model.AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.st.audit)
}

// Version returns a committed version by id.
func (m *MemoryStore) Version(id uuid.UUID) (model.Version, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.st.versions[id]
	if !ok {
		return model.Version{}, false
	}
	if a, ok := m.st.articles[v.ArticleID]; ok {
		v.TopicID, v.Kind = a.TopicID, a.Kind
	}
	return v, true
}

// Clock is a fixed clock for tests.
type Clock struct{ T time.Time }

// Now implements clock.Clock.
func (c *Clock) Now() time.Time { return c.T }

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) { c.T = c.T.Add(d) }

type memoryTx struct{ st *state }

// Audit implements port.Tx.
func (t *memoryTx) Audit(_ context.Context, e model.AuditEntry) error {
	t.st.audit = append(t.st.audit, e)
	return nil
}

// InsertTopic implements port.Tx.
func (t *memoryTx) InsertTopic(_ context.Context, topic model.Topic) (model.Topic, error) {
	for _, existing := range t.st.topics {
		if existing.Code == topic.Code {
			return model.Topic{}, model.ErrConflict
		}
	}
	t.st.topics[topic.ID] = topic
	return topic, nil
}

// GetTopic implements port.Tx.
func (t *memoryTx) GetTopic(_ context.Context, id uuid.UUID) (model.Topic, error) {
	topic, ok := t.st.topics[id]
	if !ok {
		return model.Topic{}, model.ErrNotFound
	}
	return topic, nil
}

// ListAllTopics implements port.Tx.
func (t *memoryTx) ListAllTopics(_ context.Context) ([]model.Topic, error) {
	out := make([]model.Topic, 0, len(t.st.topics))
	for _, topic := range t.st.topics {
		out = append(out, topic)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Code < out[j].Code
	})
	return out, nil
}

// ListMemberTopics implements port.Tx.
func (t *memoryTx) ListMemberTopics(_ context.Context, kind, lang string) ([]model.Topic, error) {
	have := map[uuid.UUID]bool{}
	for _, v := range t.st.versions {
		if v.Status != model.StatusPublished || v.Lang != lang {
			continue
		}
		a := t.st.articles[v.ArticleID]
		if a.Kind == kind {
			have[a.TopicID] = true
		}
	}
	out := make([]model.Topic, 0)
	for id, topic := range t.st.topics {
		if have[id] && topic.Kind == kind {
			out = append(out, topic)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}

// InsertArticle implements port.Tx.
func (t *memoryTx) InsertArticle(_ context.Context, a model.Article) (model.Article, error) {
	t.st.articles[a.ID] = a
	return a, nil
}

// GetArticle implements port.Tx.
func (t *memoryTx) GetArticle(_ context.Context, id uuid.UUID) (model.Article, error) {
	a, ok := t.st.articles[id]
	if !ok {
		return model.Article{}, model.ErrNotFound
	}
	return a, nil
}

// ListTopicArticleSummaries implements port.Tx.
func (t *memoryTx) ListTopicArticleSummaries(_ context.Context, topicID uuid.UUID, lang string, now time.Time) ([]model.ArticleSummary, error) {
	var out []model.ArticleSummary
	for _, a := range t.st.articles {
		if a.TopicID != topicID {
			continue
		}
		for _, v := range t.st.versions {
			if v.ArticleID == a.ID && v.Lang == lang && v.Status == model.StatusPublished {
				expires := time.Time{}
				if v.ExpiresAt != nil {
					expires = *v.ExpiresAt
				}
				out = append(out, model.ArticleSummary{
					ID: a.ID, Title: v.Title, Status: model.AvailabilityOf(a.Kind, expires, now),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}

// GetPublishedArticle implements port.Tx.
func (t *memoryTx) GetPublishedArticle(_ context.Context, articleID uuid.UUID, lang string, now time.Time) (model.MemberArticle, error) {
	a, ok := t.st.articles[articleID]
	if !ok {
		return model.MemberArticle{}, model.ErrNotFound
	}
	for _, v := range t.st.versions {
		if v.ArticleID == articleID && v.Lang == lang && v.Status == model.StatusPublished {
			return model.MemberArticle{
				ID: a.ID, TopicID: a.TopicID, Kind: a.Kind, Lang: lang,
				Title: v.Title, Body: v.Body, SourceURL: v.SourceURL,
				ReviewedAt: *v.ReviewedAt, ExpiresAt: *v.ExpiresAt,
				Status: model.AvailabilityOf(a.Kind, *v.ExpiresAt, now),
			}, nil
		}
	}
	return model.MemberArticle{}, model.ErrNotFound
}

// ListPublishedFragments implements port.Tx.
func (t *memoryTx) ListPublishedFragments(_ context.Context, kind, lang string, now time.Time) ([]model.Fragment, error) {
	var out []model.Fragment
	for _, a := range t.st.articles {
		if a.Kind != kind {
			continue
		}
		for _, v := range t.st.versions {
			if v.ArticleID == a.ID && v.Lang == lang && v.Status == model.StatusPublished &&
				v.ExpiresAt != nil && v.ExpiresAt.After(now) {
				out = append(out, model.Fragment{
					ArticleID: a.ID, VersionID: v.ID, Title: v.Title, SourceURL: v.SourceURL, Body: v.Body,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}

func keyOf(author uuid.UUID, key uuid.UUID) string { return author.String() + "|" + key.String() }

// InsertVersion implements port.Tx.
func (t *memoryTx) InsertVersion(_ context.Context, v model.Version) (model.Version, bool, error) {
	if v.IdempotencyKey != nil {
		if _, ok := t.st.byKey[keyOf(v.AuthorID, *v.IdempotencyKey)]; ok {
			return model.Version{}, false, nil
		}
	}
	for _, existing := range t.st.versions {
		if existing.ArticleID == v.ArticleID && existing.Lang == v.Lang &&
			(existing.Status == model.StatusDraft || existing.Status == model.StatusInReview) {
			return model.Version{}, false, model.ErrConflict
		}
	}
	t.st.versions[v.ID] = v
	if v.IdempotencyKey != nil {
		t.st.byKey[keyOf(v.AuthorID, *v.IdempotencyKey)] = v.ID
	}
	return v, true, nil
}

// GetVersionByIdempotencyKey implements port.Tx.
func (t *memoryTx) GetVersionByIdempotencyKey(_ context.Context, authorID, key uuid.UUID) (model.Version, error) {
	id, ok := t.st.byKey[keyOf(authorID, key)]
	if !ok {
		return model.Version{}, model.ErrNotFound
	}
	return t.st.versions[id], nil
}

// GetVersion implements port.Tx.
func (t *memoryTx) GetVersion(_ context.Context, id uuid.UUID, _ bool) (model.Version, error) {
	v, ok := t.st.versions[id]
	if !ok {
		return model.Version{}, model.ErrNotFound
	}
	return v, nil
}

// GetVersionWithArticle implements port.Tx.
func (t *memoryTx) GetVersionWithArticle(_ context.Context, id uuid.UUID, _ bool) (model.Version, error) {
	v, ok := t.st.versions[id]
	if !ok {
		return model.Version{}, model.ErrNotFound
	}
	a, ok := t.st.articles[v.ArticleID]
	if !ok {
		return model.Version{}, model.ErrNotFound
	}
	v.TopicID, v.Kind = a.TopicID, a.Kind
	return v, nil
}

// UpdateDraft implements port.Tx.
func (t *memoryTx) UpdateDraft(_ context.Context, id, authorID uuid.UUID, c model.Content, at time.Time) (model.Version, error) {
	v, ok := t.st.versions[id]
	if !ok || v.Status != model.StatusDraft || v.AuthorID != authorID {
		return model.Version{}, model.ErrNotFound
	}
	v.Title, v.Body, v.SourceURL, v.RejectReason, v.UpdatedAt = c.Title, c.Body, c.SourceURL, nil, at
	t.st.versions[id] = v
	return v, nil
}

// SubmitVersion implements port.Tx.
func (t *memoryTx) SubmitVersion(_ context.Context, id, authorID uuid.UUID, at time.Time) (model.Version, error) {
	v, ok := t.st.versions[id]
	if !ok || v.Status != model.StatusDraft || v.AuthorID != authorID {
		return model.Version{}, model.ErrNotFound
	}
	v.Status, v.RejectReason, v.UpdatedAt = model.StatusInReview, nil, at
	t.st.versions[id] = v
	return v, nil
}

// RejectVersion implements port.Tx.
func (t *memoryTx) RejectVersion(_ context.Context, id uuid.UUID, reason string, at time.Time) (model.Version, error) {
	v, ok := t.st.versions[id]
	if !ok || v.Status != model.StatusInReview {
		return model.Version{}, model.ErrNotFound
	}
	r := reason
	v.Status, v.RejectReason, v.UpdatedAt = model.StatusDraft, &r, at
	t.st.versions[id] = v
	return v, nil
}

// PublishVersion implements port.Tx.
func (t *memoryTx) PublishVersion(_ context.Context, id, reviewerID uuid.UUID, reviewedAt, expiresAt time.Time) (model.Version, error) {
	v, ok := t.st.versions[id]
	if !ok || v.Status != model.StatusInReview {
		return model.Version{}, model.ErrNotFound
	}
	v.Status = model.StatusPublished
	v.ReviewerID, v.ReviewedAt, v.ExpiresAt = &reviewerID, &reviewedAt, &expiresAt
	v.NeedsSync, v.RejectReason, v.UpdatedAt = false, nil, reviewedAt
	t.st.versions[id] = v
	return v, nil
}

// RetirePublished implements port.Tx.
func (t *memoryTx) RetirePublished(_ context.Context, articleID uuid.UUID, lang string, exceptID uuid.UUID, at time.Time) (model.Version, bool, error) {
	for id, v := range t.st.versions {
		if v.ArticleID == articleID && v.Lang == lang && v.Status == model.StatusPublished && id != exceptID {
			v.Status = model.StatusRetired
			v.ReviewerID, v.ReviewedAt, v.ExpiresAt, v.ReviewDueAt = nil, nil, nil, nil
			v.UpdatedAt = at
			t.st.versions[id] = v
			return v, true, nil
		}
	}
	return model.Version{}, false, nil
}

// MarkOtherLanguagesNeedsSync implements port.Tx.
func (t *memoryTx) MarkOtherLanguagesNeedsSync(_ context.Context, articleID uuid.UUID, lang string, at time.Time) error {
	for id, v := range t.st.versions {
		if v.ArticleID == articleID && v.Lang != lang && v.Status != model.StatusRetired {
			v.NeedsSync, v.UpdatedAt = true, at
			t.st.versions[id] = v
		}
	}
	return nil
}

// ListStaffVersions implements port.Tx.
func (t *memoryTx) ListStaffVersions(_ context.Context, f model.VersionFilter) ([]model.Version, error) {
	var out []model.Version
	for _, v := range t.st.versions {
		if f.Status != "" && v.Status != f.Status {
			continue
		}
		if f.CursorUpdatedAt != nil && f.CursorID != nil {
			// Keyset: keep rows strictly older than the cursor (updated_at, id).
			afterCursor := v.UpdatedAt.After(*f.CursorUpdatedAt) ||
				(v.UpdatedAt.Equal(*f.CursorUpdatedAt) && v.ID.String() >= f.CursorID.String())
			if afterCursor {
				continue
			}
		}
		a := t.st.articles[v.ArticleID]
		v.TopicID, v.Kind = a.TopicID, a.Kind
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID.String() > out[j].ID.String()
	})
	if int(f.Limit) < len(out) {
		out = out[:f.Limit]
	}
	return out, nil
}

// MarkReviewDue implements port.Tx.
func (t *memoryTx) MarkReviewDue(_ context.Context, versionID uuid.UUID, at time.Time) (model.Version, error) {
	v, ok := t.st.versions[versionID]
	if !ok || v.Status != model.StatusPublished {
		return model.Version{}, model.ErrNotFound
	}
	if v.ReviewDueAt == nil {
		due := at
		v.ReviewDueAt = &due
	}
	v.UpdatedAt = at
	t.st.versions[versionID] = v
	return v, nil
}

// ListReviewDue implements port.Tx.
func (t *memoryTx) ListReviewDue(_ context.Context, f model.ReviewDueFilter) ([]model.Version, error) {
	var out []model.Version
	for _, v := range t.st.versions {
		if v.Status != model.StatusPublished || v.ReviewDueAt == nil || v.ExpiresAt == nil {
			continue
		}
		a := t.st.articles[v.ArticleID]
		if f.Kind != "" && a.Kind != f.Kind {
			continue
		}
		if f.CursorExpiresAt != nil && f.CursorID != nil {
			beforeOrEqual := v.ExpiresAt.Before(*f.CursorExpiresAt) ||
				(v.ExpiresAt.Equal(*f.CursorExpiresAt) && v.ID.String() <= f.CursorID.String())
			if beforeOrEqual {
				continue
			}
		}
		v.TopicID, v.Kind = a.TopicID, a.Kind
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ExpiresAt.Equal(*out[j].ExpiresAt) {
			return out[i].ExpiresAt.Before(*out[j].ExpiresAt)
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	if int(f.Limit) < len(out) {
		out = out[:f.Limit]
	}
	return out, nil
}

// ListExportVersions implements port.Tx.
func (t *memoryTx) ListExportVersions(_ context.Context, now time.Time) ([]model.ExportArticle, error) {
	var out []model.ExportArticle
	for _, v := range t.st.versions {
		if v.Status != model.StatusPublished || v.ReviewedAt == nil || v.ExpiresAt == nil {
			continue
		}
		a := t.st.articles[v.ArticleID]
		if a.Kind == model.KindRights && !v.ExpiresAt.After(now) {
			continue
		}
		topic := t.st.topics[a.TopicID]
		out = append(out, model.ExportArticle{
			Lang: v.Lang, TopicCode: topic.Code, ArticleSlug: a.Slug, Kind: a.Kind,
			Title: v.Title, Body: v.Body, SourceURL: v.SourceURL,
			ReviewedAt: *v.ReviewedAt, ExpiresAt: *v.ExpiresAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lang != out[j].Lang {
			return out[i].Lang < out[j].Lang
		}
		if out[i].TopicCode != out[j].TopicCode {
			return out[i].TopicCode < out[j].TopicCode
		}
		return out[i].ArticleSlug < out[j].ArticleSlug
	})
	return out, nil
}

// Publish implements port.Tx.
func (t *memoryTx) Publish(_ context.Context, _ time.Time, e model.Event) error {
	t.st.events = append(t.st.events, e)
	return nil
}

// ScheduleReviewDue implements port.Tx.
func (t *memoryTx) ScheduleReviewDue(_ context.Context, versionID uuid.UUID, dueAt time.Time) (bool, error) {
	return t.schedule(model.TimerReviewDue, versionID, dueAt)
}

// ScheduleExpire implements port.Tx.
func (t *memoryTx) ScheduleExpire(_ context.Context, versionID uuid.UUID, dueAt time.Time) (bool, error) {
	return t.schedule(model.TimerExpire, versionID, dueAt)
}

func (t *memoryTx) schedule(kind string, versionID uuid.UUID, dueAt time.Time) (bool, error) {
	for _, existing := range t.st.timers {
		if existing.Kind == kind && existing.SubjectID == versionID && !existing.Cancelled {
			return false, nil
		}
	}
	t.st.timers = append(t.st.timers, ScheduledTimer{Kind: kind, SubjectID: versionID, DueAt: dueAt})
	return true, nil
}

// CancelVersionTimers implements port.Tx.
func (t *memoryTx) CancelVersionTimers(_ context.Context, versionID uuid.UUID, _ time.Time) error {
	for i := range t.st.timers {
		if t.st.timers[i].SubjectID == versionID {
			t.st.timers[i].Cancelled = true
		}
	}
	return nil
}
