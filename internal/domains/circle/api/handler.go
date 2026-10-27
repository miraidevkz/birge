// Package api exposes the circle HTTP handlers (OpenAPI tag circle): tea
// requests, matches and acquaintances. Authorization is enforced in the
// service; handlers validate the shape of the input and map errors to
// problem responses. Match cards never include contacts.
package api

import (
	"context"
	"errors"
	"net/http"

	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/service"
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

func toAPITea(r model.TeaRequest) TeaRequest {
	return TeaRequest{
		Id: r.ID, Format: TeaRequestFormat(r.Format), Status: TeaRequestStatus(r.Status),
		CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
	}
}

func toAPIMatch(c model.MatchCard) Match {
	ages := c.OtherChildAges
	if ages == nil {
		ages = []int{}
	}
	needs := c.SharedNeeds
	if needs == nil {
		needs = []string{}
	}
	out := Match{
		Id: c.ID, OtherDisplayName: c.OtherDisplayName, OtherDistrictId: c.OtherDistrictID,
		OtherChildAges: &ages, SharedNeeds: needs, Format: MatchFormat(c.Format),
		Status: MatchStatus(c.Status), MyAnswer: c.MyAnswer, RoomId: c.RoomID,
		AskMetAt: c.AskMetAt, CreatedAt: c.CreatedAt,
	}
	return out
}

func toAPIAcquaintance(a model.AcquaintanceView) Acquaintance {
	return Acquaintance{MemberId: a.MemberID, DisplayName: a.DisplayName, Since: a.Since}
}

// ListMyTeaRequests returns the caller's tea requests.
func (s *Server) ListMyTeaRequests(ctx context.Context, _ ListMyTeaRequestsRequestObject) (ListMyTeaRequestsResponseObject, error) {
	list, err := s.Service.ListMyTeaRequests(ctx, principal(ctx))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListMyTeaRequestsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(ListMyTeaRequests200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, toAPITea(r))
	}
	return out, nil
}

// CreateTeaRequest creates an open tea request (verified only; one open at a time).
func (s *Server) CreateTeaRequest(ctx context.Context, req CreateTeaRequestRequestObject) (CreateTeaRequestResponseObject, error) {
	fail := func(st int, c, t string) CreateTeaRequestResponseObject {
		if st == http.StatusForbidden {
			return CreateTeaRequest403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}
		}
		if st == http.StatusConflict {
			return CreateTeaRequest409ApplicationProblemPlusJSONResponse(problem(st, c, t))
		}
		return CreateTeaRequestdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil {
		return fail(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	r, err := s.Service.CreateTeaRequest(ctx, principal(ctx), req.Params.IdempotencyKey, string(req.Body.Format), model.Preferences{
		SameDistrict: req.Body.SameDistrict,
		SimilarAge:   req.Body.SimilarAge,
		SharedNeeds:  req.Body.SharedNeeds,
	})
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return CreateTeaRequest201JSONResponse(toAPITea(r)), nil
}

// CancelTeaRequest cancels the caller's open request (idempotent).
func (s *Server) CancelTeaRequest(ctx context.Context, req CancelTeaRequestRequestObject) (CancelTeaRequestResponseObject, error) {
	if err := s.Service.CancelTeaRequest(ctx, principal(ctx), req.RequestId); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return CancelTeaRequestdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return CancelTeaRequest204Response{}, nil
}

// ListMyMatches returns pair cards for the caller.
func (s *Server) ListMyMatches(ctx context.Context, _ ListMyMatchesRequestObject) (ListMyMatchesResponseObject, error) {
	list, err := s.Service.ListMyMatches(ctx, principal(ctx))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListMyMatchesdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(ListMyMatches200JSONResponse, 0, len(list))
	for _, c := range list {
		out = append(out, toAPIMatch(c))
	}
	return out, nil
}

// AnswerMatch records "Let's" / "Not now".
func (s *Server) AnswerMatch(ctx context.Context, req AnswerMatchRequestObject) (AnswerMatchResponseObject, error) {
	fail := func(st int, c, t string) AnswerMatchResponseObject {
		return AnswerMatchdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil {
		return fail(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	card, err := s.Service.AnswerMatch(ctx, principal(ctx), req.MatchId, req.Body.Accept)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return AnswerMatch200JSONResponse(toAPIMatch(card)), nil
}

// ConfirmMet records the "did you meet?" answer.
func (s *Server) ConfirmMet(ctx context.Context, req ConfirmMetRequestObject) (ConfirmMetResponseObject, error) {
	fail := func(st int, c, t string) ConfirmMetResponseObject {
		return ConfirmMetdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil {
		return fail(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	card, err := s.Service.ConfirmMet(ctx, principal(ctx), req.MatchId, req.Body.Met)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return ConfirmMet200JSONResponse(toAPIMatch(card)), nil
}

// ListAcquaintances returns members the caller met in person.
func (s *Server) ListAcquaintances(ctx context.Context, _ ListAcquaintancesRequestObject) (ListAcquaintancesResponseObject, error) {
	list, err := s.Service.ListAcquaintances(ctx, principal(ctx))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListAcquaintancesdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(ListAcquaintances200JSONResponse, 0, len(list))
	for _, a := range list {
		out = append(out, toAPIAcquaintance(a))
	}
	return out, nil
}
