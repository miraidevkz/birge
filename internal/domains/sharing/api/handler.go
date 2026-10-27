// Package api exposes the sharing HTTP handlers (OpenAPI tag sharing):
// listings, photo uploads and contact requests. Authorization is enforced in
// the service; handlers validate the shape of the input and map errors to
// problem responses.
package api

import (
	"context"
	"errors"
	"net/http"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/service"
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
	case errors.Is(err, model.ErrStorageNotConfigured):
		return http.StatusServiceUnavailable, "storage_not_configured", "Photo uploads are not available.", true
	}
	return 0, "", "", false
}

func problem(status int, code, title string) Problem {
	return Problem{Type: "about:blank", Title: title, Status: status, Code: &code}
}

func toAPIListing(v model.ListingView) Listing {
	out := Listing{
		Id: v.ID, Type: ListingType(v.Type), Category: v.Category, Title: v.Title,
		Description: v.Description, DistrictId: v.DistrictID, Status: ListingStatus(v.Status),
		CreatedAt: v.CreatedAt, ClosesAt: v.ClosesAt, RejectReason: v.RejectReason,
	}
	mine := v.Mine
	out.Mine = &mine
	if v.PhotoURL != "" {
		url := v.PhotoURL
		out.PhotoUrl = &url
	}
	return out
}

// ListListings returns the active listings feed.
func (s *Server) ListListings(ctx context.Context, req ListListingsRequestObject) (ListListingsResponseObject, error) {
	var typ *string
	if req.Params.Type != nil {
		t := string(*req.Params.Type)
		typ = &t
	}
	page, err := s.Service.ListListings(ctx, principal(ctx), typ, req.Params.Category, req.Params.DistrictId, deref(req.Params.Cursor))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListListingsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	items := make([]Listing, 0, len(page.Items))
	for _, it := range page.Items {
		items = append(items, toAPIListing(it))
	}
	out := ListingPage{Items: items}
	if page.NextCursor != "" {
		c := page.NextCursor
		out.NextCursor = &c
	}
	return ListListings200JSONResponse(out), nil
}

// CreateListing creates a pending listing.
func (s *Server) CreateListing(ctx context.Context, req CreateListingRequestObject) (CreateListingResponseObject, error) {
	fail := func(st int, c, t string) CreateListingResponseObject {
		return CreateListingdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil {
		return fail(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	v, err := s.Service.CreateListing(ctx, principal(ctx), req.Params.IdempotencyKey, model.NewListing{
		Type: string(req.Body.Type), Category: req.Body.Category, Title: req.Body.Title,
		Description: req.Body.Description, DistrictID: req.Body.DistrictId,
		PhotoUploadID: req.Body.PhotoUploadId, AcceptRules: bool(req.Body.AcceptRules),
	})
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return CreateListing201JSONResponse(toAPIListing(v)), nil
}

// CreatePhotoUpload reserves a presigned upload URL.
func (s *Server) CreatePhotoUpload(ctx context.Context, req CreatePhotoUploadRequestObject) (CreatePhotoUploadResponseObject, error) {
	fail := func(st int, c, t string) CreatePhotoUploadResponseObject {
		return CreatePhotoUploaddefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil {
		return fail(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	slot, err := s.Service.CreatePhotoUpload(ctx, principal(ctx), string(req.Body.ContentType), req.Body.SizeBytes)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return CreatePhotoUpload201JSONResponse(PhotoUpload{
		UploadId: slot.UploadID, Url: slot.URL, ExpiresAt: slot.ExpiresAt,
	}), nil
}

// ListMyListings returns the caller's listings.
func (s *Server) ListMyListings(ctx context.Context, _ ListMyListingsRequestObject) (ListMyListingsResponseObject, error) {
	list, err := s.Service.ListMyListings(ctx, principal(ctx))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListMyListingsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(ListMyListings200JSONResponse, 0, len(list))
	for _, v := range list {
		out = append(out, toAPIListing(v))
	}
	return out, nil
}

// CloseListing closes the caller's active listing.
func (s *Server) CloseListing(ctx context.Context, req CloseListingRequestObject) (CloseListingResponseObject, error) {
	if err := s.Service.CloseListing(ctx, principal(ctx), req.ListingId); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return CloseListingdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return CloseListing204Response{}, nil
}

// RequestListingContact asks the author to talk.
func (s *Server) RequestListingContact(ctx context.Context, req RequestListingContactRequestObject) (RequestListingContactResponseObject, error) {
	if err := s.Service.RequestContact(ctx, principal(ctx), req.ListingId); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return RequestListingContactdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return RequestListingContact202Response{}, nil
}

// AcceptListingContact accepts a contact request.
func (s *Server) AcceptListingContact(ctx context.Context, req AcceptListingContactRequestObject) (AcceptListingContactResponseObject, error) {
	if err := s.Service.AcceptContact(ctx, principal(ctx), req.ContactRequestId); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return AcceptListingContactdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return AcceptListingContact202Response{}, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
