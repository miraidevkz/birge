// Package api exposes the safety HTTP handlers (OpenAPI tag safety): the
// member's help request and the staff escalation queue and duty roster.
// Authorization is enforced again in the service; handlers map errors to
// problem responses and never echo names, phones or notes into errors.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"birge/internal/domains/safety/model"
	"birge/internal/domains/safety/service"
	"birge/internal/platform/auth"
)

// noStore is the Cache-Control value for responses that carry contact data.
const noStore = "no-store"

// Server implements the generated StrictServerInterface.
type Server struct {
	Service *service.Service
}

var _ StrictServerInterface = (*Server)(nil)

// principal returns the caller; the zero Principal (anonymous) is refused by
// the service with model.ErrUnauthorized.
func principal(ctx context.Context) auth.Principal {
	p, _ := auth.FromContext(ctx)
	return p
}

// problemFor maps a service error to a status, code and generic title. It
// returns ok=false for unexpected errors, which go to the 500 handler.
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

func invalid() (int, string, string) {
	return http.StatusBadRequest, "invalid_request", "The request is invalid."
}

// RequestHumanHelp opens (or returns) the member's escalation and returns the
// configured crisis lines. Idempotent per member while an escalation is open,
// so repeated presses alert nobody twice.
func (s *Server) RequestHumanHelp(ctx context.Context, req RequestHumanHelpRequestObject) (RequestHumanHelpResponseObject, error) {
	fail := func(st int, c, t string) RequestHumanHelpResponseObject {
		return RequestHumanHelpdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil || !req.Body.Source.Valid() {
		return fail(invalid()), nil
	}
	res, err := s.Service.RequestHumanHelp(ctx, principal(ctx), string(req.Body.Source))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	phones := make([]CrisisPhone, 0, len(res.CrisisPhones))
	for _, ph := range res.CrisisPhones {
		phones = append(phones, CrisisPhone{Number: ph.Number, Label: ph.Label})
	}
	return RequestHumanHelp202JSONResponse{EscalationId: res.EscalationID, CrisisPhones: phones}, nil
}

// ListEscalations returns the staff queue (psychologist or moderator).
func (s *Server) ListEscalations(ctx context.Context, req ListEscalationsRequestObject) (ListEscalationsResponseObject, error) {
	fail := func(st int, c, t string) ListEscalationsResponseObject {
		return ListEscalationsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	var status, cursor string
	if req.Params.Status != nil {
		if !req.Params.Status.Valid() {
			return fail(invalid()), nil
		}
		status = string(*req.Params.Status)
	}
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	p := principal(ctx)
	page, err := s.Service.ListEscalations(ctx, p, status, cursor)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	out := ListEscalations200JSONResponse{Items: make([]Escalation, 0, len(page.Items))}
	for _, e := range page.Items {
		out.Items = append(out.Items, toAPIEscalation(e, p.IdentityID))
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out, nil
}

// TakeEscalation makes the caller responsible for the escalation; 409 when
// someone else took it or it is closed.
func (s *Server) TakeEscalation(ctx context.Context, req TakeEscalationRequestObject) (TakeEscalationResponseObject, error) {
	p := principal(ctx)
	e, err := s.Service.TakeEscalation(ctx, p, req.EscalationId)
	if err != nil {
		st, c, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		if st == http.StatusConflict {
			return TakeEscalation409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		}
		return TakeEscalationdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	return TakeEscalation200JSONResponse(toAPIEscalation(e, p.IdentityID)), nil
}

// RevealEscalationContact returns the member's name, language and phone to
// the staff member who took the escalation, with Cache-Control: no-store.
func (s *Server) RevealEscalationContact(ctx context.Context, req RevealEscalationContactRequestObject) (RevealEscalationContactResponseObject, error) {
	c, err := s.Service.RevealContact(ctx, principal(ctx), req.EscalationId)
	if err != nil {
		st, code, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		if st == http.StatusForbidden {
			return RevealEscalationContact403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, code, t))}, nil
		}
		return RevealEscalationContactdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, code, t)}, nil
	}
	cc := noStore
	return RevealEscalationContact200JSONResponse{
		Body:    EscalationContact{DisplayName: c.DisplayName, Lang: Lang(c.Lang), Phone: c.Phone},
		Headers: RevealEscalationContact200ResponseHeaders{CacheControl: &cc},
	}, nil
}

// CloseEscalation closes an escalation the caller took, with an outcome and
// an optional note (stored sealed).
func (s *Server) CloseEscalation(ctx context.Context, req CloseEscalationRequestObject) (CloseEscalationResponseObject, error) {
	fail := func(st int, c, t string) CloseEscalationResponseObject {
		return CloseEscalationdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil || !req.Body.Outcome.Valid() {
		return fail(invalid()), nil
	}
	p := principal(ctx)
	e, err := s.Service.CloseEscalation(ctx, p, req.EscalationId, string(req.Body.Outcome), req.Body.Note)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return CloseEscalation200JSONResponse(toAPIEscalation(e, p.IdentityID)), nil
}

// GetDutyRoster returns the shifts overlapping [from, to). Phones are never returned.
func (s *Server) GetDutyRoster(ctx context.Context, req GetDutyRosterRequestObject) (GetDutyRosterResponseObject, error) {
	shifts, err := s.Service.DutyRoster(ctx, principal(ctx), req.Params.From, req.Params.To)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return GetDutyRosterdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(GetDutyRoster200JSONResponse, 0, len(shifts))
	for _, sh := range shifts {
		out = append(out, toAPIShift(sh))
	}
	return out, nil
}

// AddDutyShift adds a shift (admin only); the Idempotency-Key becomes the shift id.
func (s *Server) AddDutyShift(ctx context.Context, req AddDutyShiftRequestObject) (AddDutyShiftResponseObject, error) {
	fail := func(st int, c, t string) AddDutyShiftResponseObject {
		return AddDutyShiftdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil || !req.Body.Role.Valid() || req.Params.IdempotencyKey == uuid.Nil {
		return fail(invalid()), nil
	}
	b := req.Body
	sh, err := s.Service.AddDutyShift(ctx, principal(ctx), model.NewDutyShift{
		ID: req.Params.IdempotencyKey, StaffID: b.StaffId, Role: string(b.Role),
		StartsAt: b.StartsAt, EndsAt: b.EndsAt, Backup: b.Backup, Phone: b.Phone,
	})
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return AddDutyShift201JSONResponse(toAPIShift(sh)), nil
}

// DeleteDutyShift cancels a shift that has not started (admin only); the row
// is kept for the record. 409 when the shift has started.
func (s *Server) DeleteDutyShift(ctx context.Context, req DeleteDutyShiftRequestObject) (DeleteDutyShiftResponseObject, error) {
	if err := s.Service.CancelDutyShift(ctx, principal(ctx), req.ShiftId); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return DeleteDutyShiftdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return DeleteDutyShift204Response{}, nil
}

// toAPIEscalation exposes source, times, status and district only.
func toAPIEscalation(e model.Escalation, caller uuid.UUID) Escalation {
	out := Escalation{
		Id:       e.ID,
		Source:   EscalationSource(e.Source),
		Status:   EscalationStatus(e.Status),
		OpenedAt: e.OpenedAt,
		TakenAt:  e.TakenAt,
		ClosedAt: e.ClosedAt,
	}
	if e.DistrictID != nil {
		out.DistrictId = *e.DistrictID // uuid.Nil when unknown; see README
	}
	if e.Outcome != nil {
		o := EscalationOutcome(*e.Outcome)
		out.Outcome = &o
	}
	if e.TakenBy != nil {
		mine := caller != uuid.Nil && *e.TakenBy == caller
		out.TakenByMe = &mine
	}
	return out
}

func toAPIShift(s model.DutyShift) DutyShift {
	return DutyShift{
		Id: s.ID, StaffId: s.StaffID, Role: DutyShiftRole(s.Role),
		StartsAt: s.StartsAt, EndsAt: s.EndsAt, Backup: s.Backup,
	}
}
