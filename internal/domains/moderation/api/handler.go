// Package api exposes the moderation HTTP handlers (OpenAPI tag moderation):
// the staff queue and decisions. Authorization is enforced in the service;
// handlers map errors to problem responses and never echo content.
package api

import (
	"context"
	"errors"
	"net/http"

	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/service"
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
	case errors.Is(err, model.ErrAlreadyDecided):
		return http.StatusConflict, "already_decided", "The item is already decided.", true
	}
	return 0, "", "", false
}

func problem(status int, code, title string) Problem {
	return Problem{Type: "about:blank", Title: title, Status: status, Code: &code}
}

func toAPIItem(q model.QueueItem) ModerationItem {
	out := ModerationItem{
		Id: q.Item.ID, Kind: ModerationKind(q.Item.Kind), ObjectId: q.Item.ObjectID,
		CreatedAt: q.Item.CreatedAt, Summary: q.Summary.Text,
	}
	if q.Summary.PhotoURL != "" {
		u := q.Summary.PhotoURL
		out.PhotoUrl = &u
	}
	if q.Item.Kind == model.KindReport && q.Item.SourceReason != "" {
		r := q.Item.SourceReason
		out.ReportReason = &r
	}
	if q.Item.Kind == model.KindChatMessage && q.Item.SourceReason != "" {
		r := q.Item.SourceReason
		out.HoldReason = &r
	}
	if q.Item.Kind == model.KindReport {
		n := q.Item.ReportCount
		out.ReportCount = &n
	}
	return out
}

// GetModerationQueue returns open items, oldest first.
func (s *Server) GetModerationQueue(ctx context.Context, req GetModerationQueueRequestObject) (GetModerationQueueResponseObject, error) {
	var kind model.Kind
	if req.Params.Kind != nil {
		kind = model.Kind(*req.Params.Kind)
	}
	var cursor string
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	page, err := s.Service.ListQueue(ctx, principal(ctx), kind, cursor)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return GetModerationQueuedefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := ModerationPage{Items: make([]ModerationItem, 0, len(page.Items))}
	for _, it := range page.Items {
		out.Items = append(out.Items, toAPIItem(it))
	}
	if page.NextCursor != "" {
		c := page.NextCursor
		out.NextCursor = &c
	}
	return GetModerationQueue200JSONResponse(out), nil
}

// DecideModerationItem records a decision on an open item.
func (s *Server) DecideModerationItem(ctx context.Context, req DecideModerationItemRequestObject) (DecideModerationItemResponseObject, error) {
	fail := func(st int, c, t string) DecideModerationItemResponseObject {
		if st == http.StatusConflict {
			return DecideModerationItem409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}
		}
		return DecideModerationItemdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil {
		return fail(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	d := model.Decision{Approve: req.Body.Approve}
	if req.Body.Reason != nil {
		if !req.Body.Reason.Valid() {
			return fail(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
		}
		d.Reason = string(*req.Body.Reason)
	}
	if req.Body.BlockAuthor != nil {
		d.BlockAuthor = *req.Body.BlockAuthor
	}
	if req.Body.LegalHold != nil {
		d.LegalHold = *req.Body.LegalHold
	}
	if err := s.Service.Decide(ctx, principal(ctx), req.ItemId, d); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return DecideModerationItem204Response{}, nil
}
