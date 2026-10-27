// Package api exposes the knowledge HTTP handlers (OpenAPI tag knowledge):
// the member catalogue and the staff review workflow. Authorization is
// enforced in the service; handlers map errors to problem responses.
package api

import (
	"context"
	"errors"
	"net/http"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/service"
	"birge/internal/platform/auth"
)

// Server implements the generated StrictServerInterface.
type Server struct {
	Service *service.Service
}

var _ StrictServerInterface = (*Server)(nil)

func principal(ctx context.Context) auth.Principal {
	p, _ := auth.FromContext(ctx)
	return p
}

func problemFor(err error) (status int, code, title string, ok bool) {
	switch {
	case errors.Is(err, model.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized", "Sign in required.", true
	case errors.Is(err, model.ErrForbidden):
		return http.StatusForbidden, "forbidden", "Not allowed.", true
	case errors.Is(err, model.ErrNotFound):
		return http.StatusNotFound, "not_found", "Not found.", true
	case errors.Is(err, model.ErrInvalidInput):
		return http.StatusBadRequest, "invalid_request", "The request is invalid.", true
	case errors.Is(err, model.ErrConflict):
		return http.StatusConflict, "conflict", "The current state does not allow this.", true
	}
	return 0, "", "", false
}

func problem(status int, code, title string) Problem {
	return Problem{Type: "about:blank", Title: title, Status: status, Code: &code}
}

func toAPITopic(t model.Topic, lang string) KnowledgeTopic {
	return KnowledgeTopic{
		Id: t.ID, Kind: KnowledgeKind(t.Kind), Code: t.Code, Title: t.TitleIn(lang),
	}
}

func toAPIStaffTopic(t model.Topic) KnowledgeTopic {
	// Staff list has no lang query; prefer ru, then en, then kk, else code.
	title := t.TitleIn(model.LangRU)
	if title == t.Code {
		title = t.TitleIn(model.LangEN)
	}
	if title == t.Code {
		title = t.TitleIn(model.LangKK)
	}
	return KnowledgeTopic{Id: t.ID, Kind: KnowledgeKind(t.Kind), Code: t.Code, Title: title}
}

func toAPIStaffArticle(v model.Version) StaffArticle {
	out := StaffArticle{
		VersionId: v.ID, ArticleId: v.ArticleID, TopicId: v.TopicID,
		Kind: KnowledgeKind(v.Kind), Lang: Lang(v.Lang), Status: ArticleVersionStatus(v.Status),
		Content:  ArticleContent{Title: v.Title, Body: v.Body, SourceUrl: v.SourceURL},
		AuthorId: v.AuthorID, UpdatedAt: v.UpdatedAt,
	}
	if v.ReviewerID != nil {
		id := *v.ReviewerID
		out.ReviewerId = &id
	}
	out.ReviewedAt = v.ReviewedAt
	out.ExpiresAt = v.ExpiresAt
	if v.NeedsSync {
		ns := true
		out.NeedsSync = &ns
	}
	if v.RejectReason != nil {
		r := StaffArticleRejectReason(*v.RejectReason)
		out.RejectReason = &r
	}
	return out
}

// ListKnowledgeTopics returns topics with at least one published article in lang.
func (s *Server) ListKnowledgeTopics(ctx context.Context, req ListKnowledgeTopicsRequestObject) (ListKnowledgeTopicsResponseObject, error) {
	topics, err := s.Service.ListTopics(ctx, principal(ctx), string(req.Params.Kind), string(req.Params.Lang))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListKnowledgeTopicsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(ListKnowledgeTopics200JSONResponse, 0, len(topics))
	for _, topic := range topics {
		out = append(out, toAPITopic(topic, string(req.Params.Lang)))
	}
	return out, nil
}

// ListTopicArticles returns published articles of a topic.
func (s *Server) ListTopicArticles(ctx context.Context, req ListTopicArticlesRequestObject) (ListTopicArticlesResponseObject, error) {
	items, err := s.Service.ListTopicArticles(ctx, principal(ctx), req.TopicId, string(req.Params.Lang))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListTopicArticlesdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(ListTopicArticles200JSONResponse, 0, len(items))
	for _, a := range items {
		out = append(out, ArticleSummary{Id: a.ID, Title: a.Title, Status: ArticleAvailability(a.Status)})
	}
	return out, nil
}

// GetArticle returns one published article in lang.
func (s *Server) GetArticle(ctx context.Context, req GetArticleRequestObject) (GetArticleResponseObject, error) {
	a, err := s.Service.GetArticle(ctx, principal(ctx), req.ArticleId, string(req.Params.Lang))
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return GetArticle404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "not_found", "Not found."))}, nil
		}
		if st, c, t, ok := problemFor(err); ok {
			return GetArticledefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := Article{
		Id: a.ID, TopicId: a.TopicID, Kind: KnowledgeKind(a.Kind), Lang: Lang(a.Lang),
		Title: a.Title, Body: a.Body, SourceUrl: a.SourceURL, ReviewedAt: a.ReviewedAt,
		Status: ArticleAvailability(a.Status),
	}
	if a.Kind == model.KindRights {
		d := model.DisclaimerKeyRights
		out.DisclaimerKey = &d
	}
	return GetArticle200JSONResponse(out), nil
}

// StaffListTopics returns all topics.
func (s *Server) StaffListTopics(ctx context.Context, _ StaffListTopicsRequestObject) (StaffListTopicsResponseObject, error) {
	topics, err := s.Service.StaffListTopics(ctx, principal(ctx))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return StaffListTopicsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(StaffListTopics200JSONResponse, 0, len(topics))
	for _, topic := range topics {
		out = append(out, toAPIStaffTopic(topic))
	}
	return out, nil
}

// StaffCreateTopic creates a topic.
func (s *Server) StaffCreateTopic(ctx context.Context, req StaffCreateTopicRequestObject) (StaffCreateTopicResponseObject, error) {
	if req.Body == nil {
		st, c, t := http.StatusBadRequest, "invalid_request", "The request is invalid."
		return StaffCreateTopicdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	titles := map[string]*string{"kk": req.Body.Titles.Kk, "ru": req.Body.Titles.Ru, "en": req.Body.Titles.En}
	topic, err := s.Service.CreateTopic(ctx, principal(ctx), string(req.Body.Kind), req.Body.Code, titles)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return StaffCreateTopicdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return StaffCreateTopic201JSONResponse(toAPIStaffTopic(topic)), nil
}

// StaffListReviewDue returns published versions due for review.
func (s *Server) StaffListReviewDue(ctx context.Context, req StaffListReviewDueRequestObject) (StaffListReviewDueResponseObject, error) {
	var cursor string
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	page, err := s.Service.StaffListReviewDue(ctx, principal(ctx), cursor)
	if err != nil {
		if errors.Is(err, model.ErrForbidden) {
			return StaffListReviewDue403ApplicationProblemPlusJSONResponse{
				ProblemApplicationProblemPlusJSONResponse(problem(http.StatusForbidden, "forbidden", "Not allowed.")),
			}, nil
		}
		if st, c, t, ok := problemFor(err); ok {
			return StaffListReviewDuedefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	items := make([]StaffArticle, 0, len(page.Items))
	for _, v := range page.Items {
		items = append(items, toAPIStaffArticle(v))
	}
	out := StaffListReviewDue200JSONResponse{Items: items}
	if page.NextCursor != "" {
		c := page.NextCursor
		out.NextCursor = &c
	}
	return out, nil
}

// StaffListArticles returns a page of versions.
func (s *Server) StaffListArticles(ctx context.Context, req StaffListArticlesRequestObject) (StaffListArticlesResponseObject, error) {
	var status, cursor string
	if req.Params.Status != nil {
		status = string(*req.Params.Status)
	}
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	page, err := s.Service.StaffListArticles(ctx, principal(ctx), status, cursor)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return StaffListArticlesdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	items := make([]StaffArticle, 0, len(page.Items))
	for _, v := range page.Items {
		items = append(items, toAPIStaffArticle(v))
	}
	out := StaffListArticles200JSONResponse{Items: items}
	if page.NextCursor != "" {
		c := page.NextCursor
		out.NextCursor = &c
	}
	return out, nil
}

// StaffCreateDraft creates a draft (new article or new language version).
func (s *Server) StaffCreateDraft(ctx context.Context, req StaffCreateDraftRequestObject) (StaffCreateDraftResponseObject, error) {
	if req.Body == nil {
		st, c, t := http.StatusBadRequest, "invalid_request", "The request is invalid."
		return StaffCreateDraftdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	v, err := s.Service.CreateDraft(ctx, principal(ctx), req.Params.IdempotencyKey, req.Body.TopicId, req.Body.ArticleId, string(req.Body.Lang), model.Content{
		Title: req.Body.Content.Title, Body: req.Body.Content.Body, SourceURL: req.Body.Content.SourceUrl,
	})
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return StaffCreateDraftdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return StaffCreateDraft201JSONResponse(toAPIStaffArticle(v)), nil
}

// StaffUpdateDraft edits the caller's draft.
func (s *Server) StaffUpdateDraft(ctx context.Context, req StaffUpdateDraftRequestObject) (StaffUpdateDraftResponseObject, error) {
	if req.Body == nil {
		st, c, t := http.StatusBadRequest, "invalid_request", "The request is invalid."
		return StaffUpdateDraftdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	v, err := s.Service.UpdateDraft(ctx, principal(ctx), req.VersionId, model.Content{
		Title: req.Body.Title, Body: req.Body.Body, SourceURL: req.Body.SourceUrl,
	})
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return StaffUpdateDraftdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return StaffUpdateDraft200JSONResponse(toAPIStaffArticle(v)), nil
}

// StaffSubmitForReview moves a draft to in_review.
func (s *Server) StaffSubmitForReview(ctx context.Context, req StaffSubmitForReviewRequestObject) (StaffSubmitForReviewResponseObject, error) {
	v, err := s.Service.SubmitForReview(ctx, principal(ctx), req.VersionId)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return StaffSubmitForReviewdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return StaffSubmitForReview200JSONResponse(toAPIStaffArticle(v)), nil
}

// StaffPublish publishes an in_review version.
func (s *Server) StaffPublish(ctx context.Context, req StaffPublishRequestObject) (StaffPublishResponseObject, error) {
	v, err := s.Service.Publish(ctx, principal(ctx), req.VersionId)
	if err != nil {
		if errors.Is(err, model.ErrForbidden) {
			return StaffPublish403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(http.StatusForbidden, "forbidden", "Not allowed."))}, nil
		}
		if st, c, t, ok := problemFor(err); ok {
			return StaffPublishdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return StaffPublish200JSONResponse(toAPIStaffArticle(v)), nil
}

// StaffReject rejects an in_review version back to draft.
func (s *Server) StaffReject(ctx context.Context, req StaffRejectRequestObject) (StaffRejectResponseObject, error) {
	if req.Body == nil {
		st, c, t := http.StatusBadRequest, "invalid_request", "The request is invalid."
		return StaffRejectdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	v, err := s.Service.Reject(ctx, principal(ctx), req.VersionId, string(req.Body.Reason))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return StaffRejectdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return StaffReject200JSONResponse(toAPIStaffArticle(v)), nil
}
