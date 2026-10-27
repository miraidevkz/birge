// Package api exposes the directory HTTP handlers (OpenAPI tag directory):
// specialist search, suggest and reviews. Authorization is enforced in the
// service; handlers map errors to problem responses.
package api

import (
	"context"
	"errors"
	"net/http"

	"birge/internal/domains/directory/model"
	"birge/internal/domains/directory/service"
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

func toAPIReview(v model.ReviewView) Review {
	return Review{
		Id: v.ID, Rating: v.Rating, Text: v.Text, AuthorName: v.AuthorName,
		Status: ReviewStatus(v.Status), CreatedAt: v.CreatedAt,
	}
}

func toAPISpecialist(v model.SpecialistView) Specialist {
	needs := v.Needs
	if needs == nil {
		needs = []string{}
	}
	revs := make([]Review, 0, len(v.LatestReviews))
	for _, r := range v.LatestReviews {
		revs = append(revs, toAPIReview(r))
	}
	return Specialist{
		Id: v.ID, Name: v.Name, Profession: v.Profession, DistrictId: v.DistrictID,
		Needs: needs, Contacts: v.Contacts, Status: SpecialistStatus(v.Status),
		AverageRating: v.AverageRating, ReviewCount: v.ReviewCount, LatestReviews: revs,
	}
}

// SearchSpecialists returns active specialists.
func (s *Server) SearchSpecialists(ctx context.Context, req SearchSpecialistsRequestObject) (SearchSpecialistsResponseObject, error) {
	page, err := s.Service.SearchSpecialists(ctx, principal(ctx), req.Params.Profession, req.Params.Need, req.Params.DistrictId, deref(req.Params.Cursor))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return SearchSpecialistsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	items := make([]Specialist, 0, len(page.Items))
	for _, it := range page.Items {
		items = append(items, toAPISpecialist(it))
	}
	out := SpecialistPage{Items: items}
	if page.NextCursor != "" {
		c := page.NextCursor
		out.NextCursor = &c
	}
	return SearchSpecialists200JSONResponse(out), nil
}

// SuggestSpecialist creates a pending specialist.
func (s *Server) SuggestSpecialist(ctx context.Context, req SuggestSpecialistRequestObject) (SuggestSpecialistResponseObject, error) {
	fail := func(st int, c, t string) SuggestSpecialistResponseObject {
		return SuggestSpecialistdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil {
		return fail(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	v, err := s.Service.SuggestSpecialist(ctx, principal(ctx), req.Params.IdempotencyKey, model.NewSpecialist{
		Name: req.Body.Name, Profession: req.Body.Profession, DistrictID: req.Body.DistrictId,
		Needs: req.Body.Needs, Contacts: req.Body.Contacts,
	})
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return SuggestSpecialist201JSONResponse(toAPISpecialist(v)), nil
}

// PutReview creates or replaces the caller's review.
func (s *Server) PutReview(ctx context.Context, req PutReviewRequestObject) (PutReviewResponseObject, error) {
	fail := func(st int, c, t string) PutReviewResponseObject {
		return PutReviewdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil {
		return fail(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	v, err := s.Service.PutReview(ctx, principal(ctx), req.SpecialistId, model.NewReview{
		Rating: req.Body.Rating, Text: req.Body.Text,
	})
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return PutReview200JSONResponse(toAPIReview(v)), nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
