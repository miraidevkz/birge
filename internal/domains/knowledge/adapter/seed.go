package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/knowledge/model"
	knowledgestore "birge/internal/domains/knowledge/store"
	"birge/internal/platform/db"
)

// DemoSeed is DEMO knowledge content inserted when the knowledge tables are empty.
// Titles and bodies must contain "DEMO". IDs are assigned on insert.
type DemoSeed struct {
	Topics   []DemoTopic
	Articles []DemoArticle
}

// DemoTopic is one DEMO topic with per-language titles.
type DemoTopic struct {
	Kind    string
	Code    string
	TitleKK string
	TitleRU string
	TitleEN string
}

// DemoArticle is one DEMO article with versions in each language.
type DemoArticle struct {
	TopicCode string
	Slug      string
	Versions  []DemoVersion
}

// DemoVersion is one language version of a DEMO article (published).
type DemoVersion struct {
	Lang      string
	Title     string
	Body      string
	SourceURL string // required for rights
}

// Fixed synthetic staff ids used only for DEMO seed (author ≠ reviewer).
var (
	demoAuthorID   = uuid.MustParse("01900000-0000-7000-8000-000000000001")
	demoReviewerID = uuid.MustParse("01900000-0000-7000-8000-000000000002")
)

// SeedDemoIfEmpty inserts DEMO topics and published articles when every
// knowledge table is empty. It never overwrites. When the knowledge schema
// is not yet applied (migration pending at merge), it is a no-op.
// seeded is true only when this call inserted the seed.
func (p *PostgresStore) SeedDemoIfEmpty(ctx context.Context, seed DemoSeed, now time.Time) (bool, error) {
	return SeedDemoIfEmpty(ctx, p.Pool, seed, now)
}

// SeedDemoIfEmpty inserts DEMO knowledge content into pool when empty.
func SeedDemoIfEmpty(ctx context.Context, pool *pgxpool.Pool, seed DemoSeed, now time.Time) (seeded bool, err error) {
	if len(seed.Topics) == 0 || len(seed.Articles) == 0 {
		return false, fmt.Errorf("knowledge: demo seed needs topics and articles")
	}
	err = db.InTx(ctx, pool, func(tx pgx.Tx) error {
		q := knowledgestore.New(tx)
		present, err := q.KnowledgeSchemaExists(ctx)
		if err != nil {
			return fmt.Errorf("knowledge: schema check: %w", err)
		}
		if !present {
			return nil
		}
		empty, err := q.KnowledgeIsEmpty(ctx)
		if err != nil {
			return fmt.Errorf("knowledge: empty check: %w", err)
		}
		if !empty {
			return nil
		}
		topicByCode := make(map[string]uuid.UUID, len(seed.Topics))
		for _, t := range seed.Topics {
			if !model.KindIsValid(t.Kind) || model.ValidateCode(t.Code) != nil {
				return fmt.Errorf("knowledge: demo topic %q invalid", t.Code)
			}
			id := uuid.Must(uuid.NewV7())
			kk, ru, en := t.TitleKK, t.TitleRU, t.TitleEN
			if _, err := q.InsertTopic(ctx, knowledgestore.InsertTopicParams{
				ID: id, Kind: t.Kind, Code: t.Code, TitleKk: &kk, TitleRu: &ru, TitleEn: &en,
			}); err != nil {
				return fmt.Errorf("knowledge: insert demo topic: %w", err)
			}
			topicByCode[t.Code] = id
		}
		for _, a := range seed.Articles {
			topicID, ok := topicByCode[a.TopicCode]
			if !ok {
				return fmt.Errorf("knowledge: demo article topic %q unknown", a.TopicCode)
			}
			if model.ValidateSlug(a.Slug) != nil || !model.SlugSafe(a.Slug) {
				return fmt.Errorf("knowledge: demo article slug %q invalid", a.Slug)
			}
			var kind string
			for _, t := range seed.Topics {
				if t.Code == a.TopicCode {
					kind = t.Kind
					break
				}
			}
			aid := uuid.Must(uuid.NewV7())
			if _, err := q.InsertArticle(ctx, knowledgestore.InsertArticleParams{
				ID: aid, TopicID: topicID, Kind: kind, Slug: a.Slug, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("knowledge: insert demo article: %w", err)
			}
			ttl := model.DefaultSelfhelpTTL
			if kind == model.KindRights {
				ttl = model.DefaultRightsTTL
			}
			expires := now.Add(ttl)
			for _, v := range a.Versions {
				if !model.LangIsValid(v.Lang) {
					return fmt.Errorf("knowledge: demo version lang %q", v.Lang)
				}
				vid := uuid.Must(uuid.NewV7())
				var source *string
				if v.SourceURL != "" {
					s := v.SourceURL
					source = &s
				}
				if kind == model.KindRights && source == nil {
					return fmt.Errorf("knowledge: demo rights version needs source_url")
				}
				reviewer := demoReviewerID
				reviewed := now
				exp := expires
				// Insert as in_review then publish: seed skips authoring so
				// DEMO content is available after migrate.
				if _, err := q.InsertVersion(ctx, knowledgestore.InsertVersionParams{
					ID: vid, ArticleID: aid, Lang: v.Lang, Status: model.StatusInReview,
					Title: v.Title, Body: v.Body, SourceUrl: source, AuthorID: demoAuthorID,
					NeedsSync: false, CreatedAt: now, UpdatedAt: now,
				}); err != nil {
					return fmt.Errorf("knowledge: insert demo version: %w", err)
				}
				if _, err := q.PublishVersion(ctx, knowledgestore.PublishVersionParams{
					ReviewerID: &reviewer, ReviewedAt: &reviewed, ExpiresAt: &exp,
					UpdatedAt: now, ID: vid,
				}); err != nil {
					return fmt.Errorf("knowledge: publish demo version: %w", err)
				}
			}
		}
		seeded = true
		return nil
	})
	return seeded, err
}
