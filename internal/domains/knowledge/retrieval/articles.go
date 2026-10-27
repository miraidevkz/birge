package retrieval

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/port"
	"birge/internal/platform/clock"
)

// StoreArticles loads published versions through the knowledge store port.
type StoreArticles struct {
	Store port.Store
	Clock clock.Clock
}

var _ Articles = (*StoreArticles)(nil)

func (s *StoreArticles) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now().UTC()
	}
	return time.Now().UTC()
}

// GetPublished implements Articles.
func (s *StoreArticles) GetPublished(ctx context.Context, versionID uuid.UUID) (Article, error) {
	var out Article
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		v, err := tx.GetVersionWithArticle(ctx, versionID, false)
		if err != nil {
			return err
		}
		if v.Status != model.StatusPublished {
			return model.ErrNotFound
		}
		if model.AvailabilityOf(v.Kind, derefTime(v.ExpiresAt), s.now()) != model.AvailabilityCurrent {
			return model.ErrNotFound
		}
		out = articleFromVersion(v)
		return nil
	})
	return out, err
}

// ListPublished implements Articles.
func (s *StoreArticles) ListPublished(ctx context.Context) ([]Article, error) {
	now := s.now()
	var out []Article
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		for _, kind := range []string{model.KindRights, model.KindSelfhelp} {
			for _, lang := range []string{model.LangKK, model.LangRU, model.LangEN} {
				frags, err := tx.ListPublishedFragments(ctx, kind, lang, now)
				if err != nil {
					return err
				}
				for _, f := range frags {
					src := ""
					if f.SourceURL != nil {
						src = *f.SourceURL
					}
					out = append(out, Article{
						ArticleID: f.ArticleID,
						VersionID: f.VersionID,
						Lang:      lang,
						Kind:      kind,
						Title:     f.Title,
						SourceURL: src,
						Body:      f.Body,
					})
				}
			}
		}
		return nil
	})
	return out, err
}

func articleFromVersion(v model.Version) Article {
	src := ""
	if v.SourceURL != nil {
		src = *v.SourceURL
	}
	return Article{
		ArticleID: v.ArticleID,
		VersionID: v.ID,
		Lang:      v.Lang,
		Kind:      v.Kind,
		Title:     v.Title,
		SourceURL: src,
		Body:      v.Body,
	}
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
