// Package api exposes the membership HTTP handlers (OpenAPI tag membership).
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/service"
	"birge/internal/platform/httpapi"
)

// Server implements StrictServerInterface on the membership service.
type Server struct {
	svc *service.Service
}

var _ StrictServerInterface = (*Server)(nil)

// NewServer returns the strict server for svc.
func NewServer(svc *service.Service) *Server { return &Server{svc: svc} }

// NewHTTPHandler returns the membership routes under baseURL with problem+json
// error responses. Authentication is the session middleware's job; handlers
// read the principal from the request context.
func NewHTTPHandler(svc *service.Service, logger *slog.Logger, baseURL string) http.Handler {
	strict := NewStrictHandlerWithOptions(NewServer(svc), nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httpapi.RequestErrorHandler,
		ResponseErrorHandlerFunc: httpapi.ResponseErrorHandler(logger),
	})
	return HandlerWithOptions(strict, StdHTTPServerOptions{BaseURL: baseURL, ErrorHandlerFunc: httpapi.RequestErrorHandler})
}

// GetMe returns the signed-in member, creating her on the first call.
func (h *Server) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	m, err := h.svc.Me(ctx)
	if err != nil {
		return fail[GetMedefaultApplicationProblemPlusJSONResponse](err)
	}
	return GetMe200JSONResponse(member(m)), nil
}

// UpdateMe changes the profile.
func (h *Server) UpdateMe(ctx context.Context, req UpdateMeRequestObject) (UpdateMeResponseObject, error) {
	if req.Body == nil {
		return fail[UpdateMedefaultApplicationProblemPlusJSONResponse](model.ErrInvalidRequest)
	}
	u := model.ProfileUpdate{DisplayName: req.Body.DisplayName, DistrictID: req.Body.DistrictId}
	if req.Body.Lang != nil {
		l := model.Lang(*req.Body.Lang)
		u.Lang = &l
	}
	m, err := h.svc.UpdateMe(ctx, u)
	if err != nil {
		return fail[UpdateMedefaultApplicationProblemPlusJSONResponse](err)
	}
	return UpdateMe200JSONResponse(member(m)), nil
}

// ListBlocks lists the members blocked by the signed-in member.
func (h *Server) ListBlocks(ctx context.Context, _ ListBlocksRequestObject) (ListBlocksResponseObject, error) {
	blocks, err := h.svc.Blocks(ctx)
	if err != nil {
		return fail[ListBlocksdefaultApplicationProblemPlusJSONResponse](err)
	}
	out := make(ListBlocks200JSONResponse, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, BlockedMember{MemberId: b.MemberID, DisplayName: b.DisplayName, Since: b.Since})
	}
	return out, nil
}

// UnblockMember removes a block.
func (h *Server) UnblockMember(ctx context.Context, req UnblockMemberRequestObject) (UnblockMemberResponseObject, error) {
	if err := h.svc.Unblock(ctx, req.MemberId); err != nil {
		return fail[UnblockMemberdefaultApplicationProblemPlusJSONResponse](err)
	}
	return UnblockMember204Response{}, nil
}

// BlockMember blocks a member.
func (h *Server) BlockMember(ctx context.Context, req BlockMemberRequestObject) (BlockMemberResponseObject, error) {
	if err := h.svc.Block(ctx, req.MemberId); err != nil {
		return fail[BlockMemberdefaultApplicationProblemPlusJSONResponse](err)
	}
	return BlockMember204Response{}, nil
}

// AddChild adds a child.
func (h *Server) AddChild(ctx context.Context, req AddChildRequestObject) (AddChildResponseObject, error) {
	if req.Body == nil {
		return fail[AddChilddefaultApplicationProblemPlusJSONResponse](model.ErrInvalidRequest)
	}
	c, err := h.svc.AddChild(ctx, req.Params.IdempotencyKey, model.ChildInput{BirthYear: req.Body.BirthYear, Needs: req.Body.Needs})
	if err != nil {
		return fail[AddChilddefaultApplicationProblemPlusJSONResponse](err)
	}
	return AddChild201JSONResponse(child(c)), nil
}

// DeleteChild removes a child.
func (h *Server) DeleteChild(ctx context.Context, req DeleteChildRequestObject) (DeleteChildResponseObject, error) {
	if err := h.svc.DeleteChild(ctx, req.ChildId); err != nil {
		return fail[DeleteChilddefaultApplicationProblemPlusJSONResponse](err)
	}
	return DeleteChild204Response{}, nil
}

// UpdateChild replaces a child card.
func (h *Server) UpdateChild(ctx context.Context, req UpdateChildRequestObject) (UpdateChildResponseObject, error) {
	if req.Body == nil {
		return fail[UpdateChilddefaultApplicationProblemPlusJSONResponse](model.ErrInvalidRequest)
	}
	c, err := h.svc.UpdateChild(ctx, req.ChildId, model.ChildInput{BirthYear: req.Body.BirthYear, Needs: req.Body.Needs})
	if err != nil {
		return fail[UpdateChilddefaultApplicationProblemPlusJSONResponse](err)
	}
	return UpdateChild200JSONResponse(child(c)), nil
}

// RequestVerification asks for a verification call.
func (h *Server) RequestVerification(ctx context.Context, _ RequestVerificationRequestObject) (RequestVerificationResponseObject, error) {
	m, err := h.svc.RequestVerification(ctx)
	if err != nil {
		return fail[RequestVerificationdefaultApplicationProblemPlusJSONResponse](err)
	}
	return RequestVerification202JSONResponse(member(m)), nil
}

type districtItem = struct {
	Code string             `json:"code"`
	Id   openapi_types.UUID `json:"id"` //nolint:revive // generated field name
}

// ListDistricts returns the cities and districts. Anonymous.
func (h *Server) ListDistricts(ctx context.Context, _ ListDistrictsRequestObject) (ListDistrictsResponseObject, error) {
	cities, err := h.svc.Cities(ctx)
	if err != nil {
		return fail[ListDistrictsdefaultApplicationProblemPlusJSONResponse](err)
	}
	out := make(ListDistricts200JSONResponse, 0, len(cities))
	for _, c := range cities {
		ds := make([]districtItem, 0, len(c.Districts))
		for _, d := range c.Districts {
			ds = append(ds, districtItem{Code: d.Code, Id: d.ID})
		}
		out = append(out, City{Id: c.ID, Code: c.Code, Districts: ds})
	}
	return out, nil
}

// ListNeeds returns the needs codes. Anonymous.
func (h *Server) ListNeeds(ctx context.Context, _ ListNeedsRequestObject) (ListNeedsResponseObject, error) {
	needs, err := h.svc.Needs(ctx)
	if err != nil {
		return fail[ListNeedsdefaultApplicationProblemPlusJSONResponse](err)
	}
	if needs == nil {
		needs = []string{}
	}
	return ListNeeds200JSONResponse(needs), nil
}

func member(m model.Member) Member {
	roles := make([]MemberRoles, 0, len(m.Roles))
	for _, r := range m.Roles {
		roles = append(roles, MemberRoles(r))
	}
	children := make([]Child, 0, len(m.Children))
	for _, c := range m.Children {
		children = append(children, child(c))
	}
	var name *string
	if m.DisplayName != "" {
		name = &m.DisplayName
	}
	return Member{
		Id: m.ID, DisplayName: name, Lang: Lang(m.Lang), CityId: m.CityID, DistrictId: m.DistrictID,
		Verification: MemberVerification(m.Verification), Roles: roles, Children: children,
	}
}

func child(c model.Child) Child {
	needs := c.Needs
	if needs == nil {
		needs = []string{}
	}
	return Child{Id: c.ID, BirthYear: c.BirthYear, Needs: needs}
}

// problemResponse has the shape of every generated default (problem) response.
type problemResponse = struct {
	Body       Problem
	StatusCode int
}

// fail turns an expected service error into a problem response of type T.
// Any other error is returned as is: the response error handler logs it and
// answers 500 without details.
func fail[T ~struct {
	Body       Problem
	StatusCode int
}](err error) (T, error) {
	var e *model.Error
	if !errors.As(err, &e) {
		var zero T
		return zero, err
	}
	status := statusOf(e.Kind)
	code := e.Code
	return T(problemResponse{
		Body:       Problem{Type: "about:blank", Title: e.Title, Status: status, Code: &code},
		StatusCode: status,
	}), nil
}

func statusOf(k model.Kind) int {
	switch k {
	case model.KindInvalid:
		return http.StatusBadRequest
	case model.KindUnauthenticated:
		return http.StatusUnauthorized
	case model.KindForbidden:
		return http.StatusForbidden
	case model.KindNotFound:
		return http.StatusNotFound
	case model.KindConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
