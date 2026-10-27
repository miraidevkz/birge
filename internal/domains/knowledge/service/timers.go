package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/port"
)

// HandleReviewDue handles knowledge.ReviewDue (subject = version id): marks
// the version review_due_at so staffListReviewDue can list it. Staff push or
// SMS to reviewers is out of scope until staff devices exist; the README
// documents that follow-up. The timer completes so it does not retry.
func (s *Service) HandleReviewDue(ctx context.Context, tx port.Tx, versionID uuid.UUID) error {
	now := s.now()
	v, err := tx.GetVersionWithArticle(ctx, versionID, true)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		return err
	}
	if v.Status != model.StatusPublished {
		return nil
	}
	if _, err := tx.MarkReviewDue(ctx, versionID, now); err != nil {
		return err
	}
	s.log().Info("knowledge review due", "event", "knowledge.ReviewDue", "version_id", versionID, "article_id", v.ArticleID, "kind", v.Kind)
	return nil
}

// HandleExpire handles knowledge.Expire (subject = version id): emits
// knowledge.ArticleExpired so retrieval can drop the fragment. Member
// visibility for rights switches to being_rechecked from expires_at; the
// row stays published until a new version retires it.
func (s *Service) HandleExpire(ctx context.Context, tx port.Tx, versionID uuid.UUID) error {
	now := s.now()
	v, err := tx.GetVersionWithArticle(ctx, versionID, true)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		return err
	}
	if v.Status != model.StatusPublished {
		return nil
	}
	if err := tx.Publish(ctx, now, model.Event{
		Type:    model.EventArticleExpired,
		Version: model.EventVersion,
		Data: model.ArticleExpiredData{
			ArticleID: v.ArticleID, VersionID: versionID, Lang: v.Lang,
		},
	}); err != nil {
		return fmt.Errorf("publish expired: %w", err)
	}
	s.log().Info("article expired", "event", model.EventArticleExpired, "version_id", versionID, "article_id", v.ArticleID)
	return nil
}
