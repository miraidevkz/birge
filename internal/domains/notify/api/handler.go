// Package api exposes the notify HTTP handlers (OpenAPI tag notify).
package api

import (
	"context"
	"errors"
	"net/http"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/service"
	"birge/internal/platform/auth"
)

// Server implements the generated StrictServerInterface.
type Server struct {
	Service *service.Service
}

// ListInbox returns the caller's inbox page.
func (s *Server) ListInbox(ctx context.Context, request ListInboxRequestObject) (ListInboxResponseObject, error) {
	memberID, ok := auth.FromContext(ctx)
	if !ok || memberID.Staff {
		return listProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	var cursor string
	if request.Params.Cursor != nil {
		cursor = *request.Params.Cursor
	}
	limit := 0
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	page, err := s.Service.ListInbox(ctx, memberID.IdentityID, cursor, limit)
	if err != nil {
		if errors.Is(err, model.ErrInvalidCursor) {
			return listProblem(http.StatusBadRequest, "invalid_cursor", "The cursor is invalid."), nil
		}
		return nil, err
	}
	items := make([]Notification, 0, len(page.Items))
	for _, n := range page.Items {
		items = append(items, toAPINotification(n))
	}
	resp := ListInbox200JSONResponse{Items: items}
	if page.NextCursor != "" {
		c := page.NextCursor
		resp.NextCursor = &c
	}
	return resp, nil
}

// MarkRead marks a notification as read (and acknowledges critical ones).
func (s *Server) MarkRead(ctx context.Context, request MarkReadRequestObject) (MarkReadResponseObject, error) {
	p, ok := auth.FromContext(ctx)
	if !ok || p.Staff {
		return markReadProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	err := s.Service.MarkRead(ctx, p.IdentityID, request.NotificationId)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return markReadProblem(http.StatusNotFound, "not_found", "Notification not found."), nil
		}
		return nil, err
	}
	return MarkRead204Response{}, nil
}

// RegisterDevice registers or updates a push device.
func (s *Server) RegisterDevice(ctx context.Context, request RegisterDeviceRequestObject) (RegisterDeviceResponseObject, error) {
	p, ok := auth.FromContext(ctx)
	if !ok || p.Staff {
		return registerProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	if request.Body == nil {
		return registerProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	err := s.Service.RegisterDevice(ctx, p.IdentityID, request.DeviceId, string(request.Body.Platform), request.Body.PushToken)
	if err != nil {
		if errors.Is(err, model.ErrInvalidInput) {
			return registerProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
		}
		return nil, err
	}
	return RegisterDevice204Response{}, nil
}

// UnregisterDevice removes a push device.
func (s *Server) UnregisterDevice(ctx context.Context, request UnregisterDeviceRequestObject) (UnregisterDeviceResponseObject, error) {
	p, ok := auth.FromContext(ctx)
	if !ok || p.Staff {
		return unregisterProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	if err := s.Service.UnregisterDevice(ctx, p.IdentityID, request.DeviceId); err != nil {
		return nil, err
	}
	return UnregisterDevice204Response{}, nil
}

func toAPINotification(n model.Notification) Notification {
	out := Notification{
		Id:        n.ID,
		Template:  n.Template,
		Params:    n.Params,
		Class:     NotificationClass(n.Class),
		CreatedAt: n.CreatedAt,
		ReadAt:    n.ReadAt,
	}
	if out.Params == nil {
		out.Params = map[string]string{}
	}
	return out
}

func listProblem(status int, code, title string) ListInboxdefaultApplicationProblemPlusJSONResponse {
	return ListInboxdefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func markReadProblem(status int, code, title string) MarkReaddefaultApplicationProblemPlusJSONResponse {
	return MarkReaddefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func registerProblem(status int, code, title string) RegisterDevicedefaultApplicationProblemPlusJSONResponse {
	return RegisterDevicedefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func unregisterProblem(status int, code, title string) UnregisterDevicedefaultApplicationProblemPlusJSONResponse {
	return UnregisterDevicedefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

var _ StrictServerInterface = (*Server)(nil)
