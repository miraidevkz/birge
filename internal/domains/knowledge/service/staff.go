package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/port"
	"birge/internal/platform/auth"
)

// StaffListTopics returns every topic (staff catalogue).
func (s *Service) StaffListTopics(ctx context.Context, p auth.Principal) ([]model.Topic, error) {
	if _, err := authorize(p, staffReadRoles); err != nil {
		return nil, err
	}
	var out []model.Topic
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		out, err = tx.ListAllTopics(ctx)
		return err
	})
	return out, err
}

// CreateTopic creates a topic. Admin may create any kind; psychologist only
// self-help; lawyer only rights.
func (s *Service) CreateTopic(ctx context.Context, p auth.Principal, kind, code string, titles map[string]*string) (model.Topic, error) {
	if !model.KindIsValid(kind) {
		return model.Topic{}, model.ErrInvalidInput
	}
	allowed := []string{model.RoleAdmin, model.ReviewerRoleFor(kind)}
	actor, err := authorize(p, allowed)
	if err != nil {
		return model.Topic{}, err
	}
	if err := model.ValidateCode(code); err != nil {
		return model.Topic{}, err
	}
	kk, ru, en := titles["kk"], titles["ru"], titles["en"]
	if err := model.ValidateTopicTitle(kk); err != nil {
		return model.Topic{}, err
	}
	if err := model.ValidateTopicTitle(ru); err != nil {
		return model.Topic{}, err
	}
	if err := model.ValidateTopicTitle(en); err != nil {
		return model.Topic{}, err
	}
	if kk == nil && ru == nil && en == nil {
		return model.Topic{}, fmt.Errorf("%w: titles", model.ErrInvalidInput)
	}
	id := uuid.Must(uuid.NewV7())
	now := s.now()
	var out model.Topic
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		created, err := tx.InsertTopic(ctx, model.Topic{
			ID: id, Kind: kind, Code: code, TitleKK: kk, TitleRU: ru, TitleEN: en, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if err := tx.Audit(ctx, auditEntry(now, actor, model.AuditTopicCreate, model.AuditObjectTopic, id, "")); err != nil {
			return err
		}
		out = created
		return nil
	})
	return out, err
}

// StaffListArticles returns one page of versions, newest first.
func (s *Service) StaffListArticles(ctx context.Context, p auth.Principal, status, cursor string) (model.StaffPage, error) {
	if _, err := authorize(p, staffReadRoles); err != nil {
		return model.StaffPage{}, err
	}
	if status != "" && !model.StatusIsValid(status) {
		return model.StaffPage{}, fmt.Errorf("%w: status", model.ErrInvalidInput)
	}
	f := model.VersionFilter{Status: status, Limit: staffPageSize + 1}
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return model.StaffPage{}, err
		}
		f.CursorUpdatedAt, f.CursorID = &at, &id
	}
	var items []model.Version
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		items, err = tx.ListStaffVersions(ctx, f)
		return err
	}); err != nil {
		return model.StaffPage{}, err
	}
	page := model.StaffPage{Items: items}
	if len(items) > staffPageSize {
		page.Items = items[:staffPageSize]
		last := page.Items[staffPageSize-1]
		page.NextCursor = encodeCursor(last.UpdatedAt, last.ID)
	}
	return page, nil
}

// StaffListReviewDue returns published versions whose ReviewDue timer has
// fired, oldest expiry first. Lawyer sees rights; psychologist self-help;
// admin both. MFA required; deny by default.
func (s *Service) StaffListReviewDue(ctx context.Context, p auth.Principal, cursor string) (model.StaffPage, error) {
	kind, err := authorizeReviewDue(p)
	if err != nil {
		return model.StaffPage{}, err
	}
	f := model.ReviewDueFilter{Kind: kind, Limit: staffPageSize + 1}
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return model.StaffPage{}, err
		}
		f.CursorExpiresAt, f.CursorID = &at, &id
	}
	var items []model.Version
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		items, err = tx.ListReviewDue(ctx, f)
		return err
	}); err != nil {
		return model.StaffPage{}, err
	}
	page := model.StaffPage{Items: items}
	if len(items) > staffPageSize {
		page.Items = items[:staffPageSize]
		last := page.Items[staffPageSize-1]
		if last.ExpiresAt == nil {
			return model.StaffPage{}, fmt.Errorf("review-due: missing expires_at")
		}
		page.NextCursor = encodeCursor(*last.ExpiresAt, last.ID)
	}
	return page, nil
}

// authorizeReviewDue returns the kind filter for the review-due queue (empty =
// both kinds for admin) or an error. MFA is required.
func authorizeReviewDue(p auth.Principal) (kind string, err error) {
	if p.IdentityID == uuid.Nil {
		return "", model.ErrUnauthorized
	}
	if !p.Staff || !p.MFA {
		return "", model.ErrForbidden
	}
	hasAdmin, hasLawyer, hasPsych := false, false, false
	for _, r := range p.Roles {
		switch r {
		case model.RoleAdmin:
			hasAdmin = true
		case model.RoleLawyer:
			hasLawyer = true
		case model.RolePsychologist:
			hasPsych = true
		}
	}
	if hasAdmin {
		return "", nil
	}
	if hasLawyer && hasPsych {
		return "", nil
	}
	if hasLawyer {
		return model.KindRights, nil
	}
	if hasPsych {
		return model.KindSelfhelp, nil
	}
	return "", model.ErrForbidden
}

// CreateDraft starts a new article (or a new language version of an existing
// one) as a draft. Psychologist authors self-help; lawyer authors rights.
// Idempotent per author and Idempotency-Key.
func (s *Service) CreateDraft(ctx context.Context, p auth.Principal, key uuid.UUID, topicID uuid.UUID, articleID *uuid.UUID, lang string, content model.Content) (model.Version, error) {
	if key == uuid.Nil {
		return model.Version{}, fmt.Errorf("%w: idempotency key", model.ErrInvalidInput)
	}
	if topicID == uuid.Nil || !model.LangIsValid(lang) {
		return model.Version{}, model.ErrInvalidInput
	}
	if err := model.ValidateContent(content); err != nil {
		return model.Version{}, err
	}
	now := s.now()
	var out model.Version
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		topic, err := tx.GetTopic(ctx, topicID)
		if err != nil {
			return err
		}
		actor, err := authorize(p, []string{model.ReviewerRoleFor(topic.Kind)})
		if err != nil {
			return err
		}
		// Replay: return the stored draft unchanged.
		if existing, err := tx.GetVersionByIdempotencyKey(ctx, actor.ID, key); err == nil {
			full, err := tx.GetVersionWithArticle(ctx, existing.ID, false)
			if err != nil {
				return err
			}
			out = full
			return nil
		} else if !errors.Is(err, model.ErrNotFound) {
			return err
		}

		var art model.Article
		if articleID != nil {
			art, err = tx.GetArticle(ctx, *articleID)
			if err != nil {
				return err
			}
			if art.TopicID != topicID || art.Kind != topic.Kind {
				return fmt.Errorf("%w: article topic", model.ErrInvalidInput)
			}
		} else {
			aid := uuid.Must(uuid.NewV7())
			slug := model.ArticleSlugFromID(aid)
			art, err = tx.InsertArticle(ctx, model.Article{
				ID: aid, TopicID: topicID, Kind: topic.Kind, Slug: slug, CreatedAt: now,
			})
			if err != nil {
				return err
			}
		}
		vid := uuid.Must(uuid.NewV7())
		keyCopy := key
		src := trimSource(content.SourceURL)
		created, inserted, err := tx.InsertVersion(ctx, model.Version{
			ID: vid, ArticleID: art.ID, TopicID: topicID, Kind: topic.Kind,
			Lang: lang, Status: model.StatusDraft, Title: strings.TrimSpace(content.Title),
			Body: strings.TrimSpace(content.Body), SourceURL: src, AuthorID: actor.ID,
			IdempotencyKey: &keyCopy, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := tx.GetVersionByIdempotencyKey(ctx, actor.ID, key)
			if err != nil {
				return err
			}
			created = existing
		}
		full, err := tx.GetVersionWithArticle(ctx, created.ID, false)
		if err != nil {
			return err
		}
		out = full
		return nil
	})
	return out, err
}

// UpdateDraft edits the caller's own draft.
func (s *Service) UpdateDraft(ctx context.Context, p auth.Principal, versionID uuid.UUID, content model.Content) (model.Version, error) {
	if versionID == uuid.Nil {
		return model.Version{}, model.ErrInvalidInput
	}
	if err := model.ValidateContent(content); err != nil {
		return model.Version{}, err
	}
	now := s.now()
	var out model.Version
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		cur, err := tx.GetVersionWithArticle(ctx, versionID, true)
		if err != nil {
			return err
		}
		actor, err := authorize(p, []string{model.ReviewerRoleFor(cur.Kind)})
		if err != nil {
			return err
		}
		if cur.Status != model.StatusDraft {
			return fmt.Errorf("%w: not a draft", model.ErrConflict)
		}
		if cur.AuthorID != actor.ID {
			return model.ErrForbidden
		}
		updated, err := tx.UpdateDraft(ctx, versionID, actor.ID, model.Content{
			Title: strings.TrimSpace(content.Title), Body: strings.TrimSpace(content.Body),
			SourceURL: trimSource(content.SourceURL),
		}, now)
		if err != nil {
			return err
		}
		updated.TopicID, updated.Kind = cur.TopicID, cur.Kind
		out = updated
		return nil
	})
	return out, err
}

// SubmitForReview moves the caller's draft to in_review.
func (s *Service) SubmitForReview(ctx context.Context, p auth.Principal, versionID uuid.UUID) (model.Version, error) {
	if versionID == uuid.Nil {
		return model.Version{}, model.ErrInvalidInput
	}
	now := s.now()
	var out model.Version
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		cur, err := tx.GetVersionWithArticle(ctx, versionID, true)
		if err != nil {
			return err
		}
		actor, err := authorize(p, []string{model.ReviewerRoleFor(cur.Kind)})
		if err != nil {
			return err
		}
		if cur.Status != model.StatusDraft {
			return fmt.Errorf("%w: not a draft", model.ErrConflict)
		}
		if cur.AuthorID != actor.ID {
			return model.ErrForbidden
		}
		updated, err := tx.SubmitVersion(ctx, versionID, actor.ID, now)
		if err != nil {
			return err
		}
		updated.TopicID, updated.Kind = cur.TopicID, cur.Kind
		out = updated
		return nil
	})
	return out, err
}

// Publish publishes an in_review version. The reviewer must hold the matching
// role and must not be the author. Rights require a source URL. Retires the
// previous published version of the same article and language, marks other
// languages needs_sync, schedules ReviewDue and Expire timers, audits and
// emits knowledge.ArticlePublished — all in one transaction.
func (s *Service) Publish(ctx context.Context, p auth.Principal, versionID uuid.UUID) (model.Version, error) {
	if versionID == uuid.Nil {
		return model.Version{}, model.ErrInvalidInput
	}
	now := s.now()
	var out model.Version
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		cur, err := tx.GetVersionWithArticle(ctx, versionID, true)
		if err != nil {
			return err
		}
		role := model.ReviewerRoleFor(cur.Kind)
		actor, err := authorize(p, []string{role})
		if err != nil {
			return err
		}
		if cur.Status != model.StatusInReview {
			return fmt.Errorf("%w: not in review", model.ErrConflict)
		}
		if cur.AuthorID == actor.ID {
			return model.ErrForbidden
		}
		if cur.Kind == model.KindRights {
			if cur.SourceURL == nil || strings.TrimSpace(*cur.SourceURL) == "" {
				return fmt.Errorf("%w: sourceUrl required for rights", model.ErrInvalidInput)
			}
		}
		expiresAt := now.Add(s.ttlFor(cur.Kind))
		retired, ok, err := tx.RetirePublished(ctx, cur.ArticleID, cur.Lang, versionID, now)
		if err != nil {
			return err
		}
		if ok {
			if err := tx.CancelVersionTimers(ctx, retired.ID, now); err != nil {
				return err
			}
		}
		published, err := tx.PublishVersion(ctx, versionID, actor.ID, now, expiresAt)
		if err != nil {
			return err
		}
		if err := tx.MarkOtherLanguagesNeedsSync(ctx, cur.ArticleID, cur.Lang, now); err != nil {
			return err
		}
		reviewDueAt := expiresAt.Add(-model.ReviewDueLead)
		if reviewDueAt.After(now) {
			if _, err := tx.ScheduleReviewDue(ctx, versionID, reviewDueAt); err != nil {
				return err
			}
		}
		if _, err := tx.ScheduleExpire(ctx, versionID, expiresAt); err != nil {
			return err
		}
		if err := tx.Publish(ctx, now, model.Event{
			Type:    model.EventArticlePublished,
			Version: model.EventVersion,
			Data: model.ArticlePublishedData{
				ArticleID: cur.ArticleID, VersionID: versionID, Lang: cur.Lang, Kind: cur.Kind,
			},
		}); err != nil {
			return err
		}
		if err := tx.Audit(ctx, auditEntry(now, actor, model.AuditPublish, model.AuditObjectVersion, versionID, "")); err != nil {
			return err
		}
		published.TopicID, published.Kind = cur.TopicID, cur.Kind
		out = published
		return nil
	})
	if err != nil {
		return model.Version{}, err
	}
	s.log().Info("article published", "event", model.EventArticlePublished, "version_id", versionID, "article_id", out.ArticleID)
	return out, nil
}

// Reject sends an in_review version back to draft with a reason code. The
// reviewer must hold the matching role and must not be the author.
func (s *Service) Reject(ctx context.Context, p auth.Principal, versionID uuid.UUID, reason string) (model.Version, error) {
	if versionID == uuid.Nil || !model.RejectIsValid(reason) {
		return model.Version{}, model.ErrInvalidInput
	}
	now := s.now()
	var out model.Version
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		cur, err := tx.GetVersionWithArticle(ctx, versionID, true)
		if err != nil {
			return err
		}
		actor, err := authorize(p, []string{model.ReviewerRoleFor(cur.Kind)})
		if err != nil {
			return err
		}
		if cur.Status != model.StatusInReview {
			return fmt.Errorf("%w: not in review", model.ErrConflict)
		}
		if cur.AuthorID == actor.ID {
			return model.ErrForbidden
		}
		updated, err := tx.RejectVersion(ctx, versionID, reason, now)
		if err != nil {
			return err
		}
		if err := tx.Audit(ctx, auditEntry(now, actor, model.AuditReject, model.AuditObjectVersion, versionID, reason)); err != nil {
			return err
		}
		updated.TopicID, updated.Kind = cur.TopicID, cur.Kind
		out = updated
		return nil
	})
	return out, err
}

func trimSource(u *string) *string {
	if u == nil {
		return nil
	}
	s := strings.TrimSpace(*u)
	if s == "" {
		return nil
	}
	return &s
}
