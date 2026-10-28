package staffdir

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"birge/internal/platform/staffdir/api"
)

// Server adapts Service to the generated strict server interface (tag staff).
type Server struct {
	Service *Service
}

var _ api.StrictServerInterface = (*Server)(nil)

// ListAuditEntries implements listAuditEntries.
func (h *Server) ListAuditEntries(ctx context.Context, req api.ListAuditEntriesRequestObject) (api.ListAuditEntriesResponseObject, error) {
	p := req.Params
	f := AuditFilter{From: p.From, To: p.To}
	if p.ActorId != nil {
		f.ActorID = *p.ActorId
	}
	if p.ObjectType != nil {
		f.ObjectType = *p.ObjectType
	}
	if p.ObjectId != nil {
		f.ObjectID = *p.ObjectId
	}
	if p.Action != nil {
		f.Action = *p.Action
	}
	if p.Cursor != nil {
		f.Cursor = *p.Cursor
	}
	page, err := h.Service.ListAuditEntries(ctx, f)
	if err != nil {
		st, prob, ok := problemOf(err)
		switch {
		case !ok:
			return nil, err
		case st == http.StatusForbidden:
			return api.ListAuditEntries403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(prob)}, nil
		default:
			return api.ListAuditEntriesdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: prob}, nil
		}
	}
	items := make([]api.AuditEntry, 0, len(page.Items))
	for _, e := range page.Items {
		item := api.AuditEntry{
			Id: e.ID, OccurredAt: e.OccurredAt, ActorId: e.ActorID, ActorRole: api.AuditActorRole(e.ActorRole),
			Action: e.Action, ObjectType: e.ObjectType,
			ActorName: optional(e.ActorName), Reason: optional(e.Reason),
		}
		if e.ObjectID != uuid.Nil {
			id := e.ObjectID
			item.ObjectId = &id
		}
		items = append(items, item)
	}
	return api.ListAuditEntries200JSONResponse(api.AuditPage{Items: items, NextCursor: optional(page.NextCursor)}), nil
}

// ListStaffAccounts implements listStaffAccounts.
func (h *Server) ListStaffAccounts(ctx context.Context, _ api.ListStaffAccountsRequestObject) (api.ListStaffAccountsResponseObject, error) {
	accounts, err := h.Service.ListStaffAccounts(ctx)
	if err != nil {
		st, prob, ok := problemOf(err)
		switch {
		case !ok:
			return nil, err
		case st == http.StatusForbidden:
			return api.ListStaffAccounts403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(prob)}, nil
		default:
			return api.ListStaffAccountsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: prob}, nil
		}
	}
	items := make([]api.StaffAccount, 0, len(accounts))
	for _, a := range accounts {
		items = append(items, staffAccount(a))
	}
	return api.ListStaffAccounts200JSONResponse(api.StaffAccountList{Items: items}), nil
}

// GetStaffAccount implements getStaffAccount.
func (h *Server) GetStaffAccount(ctx context.Context, req api.GetStaffAccountRequestObject) (api.GetStaffAccountResponseObject, error) {
	card, err := h.Service.GetStaffAccount(ctx, req.StaffId)
	if err != nil {
		st, prob, ok := problemOf(err)
		switch {
		case !ok:
			return nil, err
		case st == http.StatusForbidden:
			return api.GetStaffAccount403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(prob)}, nil
		case st == http.StatusNotFound:
			return api.GetStaffAccount404ApplicationProblemPlusJSONResponse(prob), nil
		default:
			return api.GetStaffAccountdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: prob}, nil
		}
	}
	resp := api.StaffAccountCard{Account: staffAccount(card.Account)}
	resp.Actions30d = make([]struct {
		Action api.AuditCode `json:"action"`
		Count  int           `json:"count"`
	}, 0, len(card.Actions))
	for _, c := range card.Actions {
		resp.Actions30d = append(resp.Actions30d, struct {
			Action api.AuditCode `json:"action"`
			Count  int           `json:"count"`
		}{Action: c.Action, Count: c.Count})
	}
	return api.GetStaffAccount200JSONResponse(resp), nil
}

func staffAccount(a Account) api.StaffAccount {
	roles := make([]api.StaffRole, 0, len(a.Roles))
	for _, r := range a.Roles {
		roles = append(roles, api.StaffRole(r))
	}
	return api.StaffAccount{
		Id: a.ID, DisplayName: optional(a.DisplayName), Roles: roles,
		FirstSeenAt: a.FirstSeenAt, LastSeenAt: a.LastSeenAt,
	}
}

// problemOf maps a Service error to a status and problem body; ok is false
// for unexpected errors, which go to the generic 500 handler (logged there
// without details in the response).
func problemOf(err error) (int, api.Problem, bool) {
	var st int
	var code, title string
	switch {
	case errors.Is(err, ErrUnauthenticated):
		st, code, title = http.StatusUnauthorized, "unauthenticated", "Sign in again."
	case errors.Is(err, ErrForbidden):
		st, code, title = http.StatusForbidden, "forbidden", "Only an admin can see this."
	case errors.Is(err, ErrNotFound):
		st, code, title = http.StatusNotFound, "not_found", "No such staff account."
	case errors.Is(err, ErrInvalid):
		st, code, title = http.StatusBadRequest, "invalid_request", "The request is invalid."
	default:
		return 0, api.Problem{}, false
	}
	return st, api.Problem{Type: "about:blank", Title: title, Status: st, Code: &code}, true
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
