// Package api exposes the membership HTTP handlers (OpenAPI tag membership).
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
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

// GetMemberSuspension returns a member's suspension state (staff).
func (h *Server) GetMemberSuspension(ctx context.Context, req GetMemberSuspensionRequestObject) (GetMemberSuspensionResponseObject, error) {
	s, err := h.svc.GetSuspension(ctx, req.MemberId)
	if err != nil {
		if st, c, t, ok := staffProblem(err); ok {
			if st == http.StatusForbidden {
				return GetMemberSuspension403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
			}
			return GetMemberSuspensiondefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return fail[GetMemberSuspensiondefaultApplicationProblemPlusJSONResponse](err)
	}
	out := MemberSuspension{MemberId: s.MemberID, Suspended: s.Suspended}
	if s.SuspendedAt != nil {
		at := *s.SuspendedAt
		out.SuspendedAt = &at
	}
	if s.Reason != "" {
		r := s.Reason
		out.Reason = &r
	}
	return GetMemberSuspension200JSONResponse(out), nil
}

// LiftMemberSuspension lifts a suspension (staff admin).
func (h *Server) LiftMemberSuspension(ctx context.Context, req LiftMemberSuspensionRequestObject) (LiftMemberSuspensionResponseObject, error) {
	if err := h.svc.LiftSuspension(ctx, req.MemberId); err != nil {
		if st, c, t, ok := staffProblem(err); ok {
			if st == http.StatusForbidden {
				return LiftMemberSuspension403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
			}
			return LiftMemberSuspensiondefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return fail[LiftMemberSuspensiondefaultApplicationProblemPlusJSONResponse](err)
	}
	return LiftMemberSuspension204Response{}, nil
}

// StaffListMembers lists verified or suspended members for staff.
func (h *Server) StaffListMembers(ctx context.Context, req StaffListMembersRequestObject) (StaffListMembersResponseObject, error) {
	var districtID *uuid.UUID
	if req.Params.DistrictId != nil {
		id := *req.Params.DistrictId
		districtID = &id
	}
	var cursor string
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	filter := model.StaffMemberListFilter{
		Status:     string(req.Params.Status),
		DistrictID: districtID,
		Cursor:     cursor,
	}
	page, err := h.svc.StaffListMembers(ctx, filter)
	if err != nil {
		if st, c, t, ok := staffProblem(err); ok {
			if st == http.StatusForbidden {
				return StaffListMembers403ApplicationProblemPlusJSONResponse{
					ProblemApplicationProblemPlusJSONResponse: ProblemApplicationProblemPlusJSONResponse(problem(st, c, t)),
				}, nil
			}
			return StaffListMembersdefaultApplicationProblemPlusJSONResponse{
				StatusCode: st,
				Body:       problem(st, c, t),
			}, nil
		}
		return fail[StaffListMembersdefaultApplicationProblemPlusJSONResponse](err)
	}

	items := make([]StaffMember, 0, len(page.Items))
	for _, m := range page.Items {
		item := StaffMember{
			Id:          m.ID,
			DisplayName: m.DisplayName,
			Verified:    m.Verified,
			Suspended:   m.Suspended,
		}
		if m.DistrictID != nil {
			dID := *m.DistrictID
			item.DistrictId = &dID
		}
		if m.VerifiedAt != nil {
			vAt := *m.VerifiedAt
			item.VerifiedAt = &vAt
		}
		if m.SuspendedAt != nil {
			sAt := *m.SuspendedAt
			item.SuspendedAt = &sAt
		}
		items = append(items, item)
	}
	resp := StaffMemberPage{
		Items: items,
	}
	if page.NextCursor != "" {
		c := page.NextCursor
		resp.NextCursor = &c
	}
	return StaffListMembers200JSONResponse(resp), nil
}

func problem(status int, code, title string) Problem {
	return Problem{Type: "about:blank", Title: title, Status: status, Code: &code}
}

func staffProblem(err error) (status int, code, title string, ok bool) {
	var e *model.Error
	if !errors.As(err, &e) {
		return 0, "", "", false
	}
	return statusOf(e.Kind), e.Code, e.Title, true
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
