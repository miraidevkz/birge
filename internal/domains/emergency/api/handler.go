// Package api exposes the emergency HTTP handlers (OpenAPI tag emergency):
// SOS, the district SOS feed and responses, the emergency plan, the trusted
// contact's view of an SOS and her invitation, and the moderators' SOS
// queue under /staff/sos (mounted behind staffauth by the wiring).
// Authorization is enforced in the service; handlers validate the shape of
// the input, map errors to problem responses and never echo texts, names or
// phones into errors. External contacts' phones leave only masked.
package api

import (
	"context"
	"errors"
	"net/http"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/service"
	"birge/internal/platform/auth"
)

// noStore is the Cache-Control value for the shared plan (a child card).
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
	case errors.Is(err, model.ErrRateLimited):
		return http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", true
	}
	return 0, "", "", false
}

func problem(status int, code, title string) Problem {
	return Problem{Type: "about:blank", Title: title, Status: status, Code: &code}
}

func invalid() (int, string, string) {
	return http.StatusBadRequest, "invalid_request", "The request is invalid."
}

func toAPISos(v model.SosView) SosRequest {
	closes := v.ClosesAt
	return SosRequest{
		Id: v.ID, Type: SosType(v.Type), Text: v.PlainText, Status: SosRequestStatus(v.Status),
		SharedToDistrict: v.Shared(), CreatedAt: v.CreatedAt, ClosesAt: &closes, ResponseCount: v.ResponseCount,
	}
}

func toAPIContact(c model.ContactView) TrustedContact {
	out := TrustedContact{
		Id: c.ID, Kind: TrustedContactKind(c.Kind), Status: TrustedContactStatus(c.Status), DisplayName: c.DisplayName,
	}
	if c.PhoneMasked != "" {
		masked := c.PhoneMasked
		out.PhoneMasked = &masked
	}
	return out
}

// GetEmergencyPlan returns the caller's plan (child card, contacts with
// masked phones, last send time).
func (s *Server) GetEmergencyPlan(ctx context.Context, _ GetEmergencyPlanRequestObject) (GetEmergencyPlanResponseObject, error) {
	plan, err := s.Service.GetPlan(ctx, principal(ctx))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return GetEmergencyPlandefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := GetEmergencyPlan200JSONResponse{ChildCard: plan.ChildCard, Contacts: make([]TrustedContact, 0, len(plan.Contacts)), LastSentAt: plan.LastSentAt}
	for _, c := range plan.Contacts {
		out.Contacts = append(out.Contacts, toAPIContact(c))
	}
	return out, nil
}

// SetChildCard stores the caller's child card (sealed).
func (s *Server) SetChildCard(ctx context.Context, req SetChildCardRequestObject) (SetChildCardResponseObject, error) {
	fail := func(st int, c, t string) SetChildCardResponseObject {
		return SetChildCarddefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil {
		return fail(invalid()), nil
	}
	if err := s.Service.SetChildCard(ctx, principal(ctx), req.Body.Text); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return SetChildCard204Response{}, nil
}

// SendEmergencyPlan sends the child card to the confirmed trusted contacts
// after the confirmation "SEND"; idempotent per Idempotency-Key.
func (s *Server) SendEmergencyPlan(ctx context.Context, req SendEmergencyPlanRequestObject) (SendEmergencyPlanResponseObject, error) {
	fail := func(st int, c, t string) SendEmergencyPlanResponseObject {
		return SendEmergencyPlandefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil || !req.Body.Confirm.Valid() {
		return fail(invalid()), nil
	}
	if err := s.Service.SendPlan(ctx, principal(ctx), req.Params.IdempotencyKey, string(req.Body.Confirm)); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return SendEmergencyPlan202Response{}, nil
}

// ListMySos returns the caller's latest SOS requests.
func (s *Server) ListMySos(ctx context.Context, _ ListMySosRequestObject) (ListMySosResponseObject, error) {
	list, err := s.Service.ListMySos(ctx, principal(ctx))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListMySosdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(ListMySos200JSONResponse, 0, len(list))
	for _, v := range list {
		out = append(out, toAPISos(v))
	}
	return out, nil
}

// RaiseSos raises an SOS; 429 after three in an hour.
func (s *Server) RaiseSos(ctx context.Context, req RaiseSosRequestObject) (RaiseSosResponseObject, error) {
	fail := func(st int, c, t string) RaiseSosResponseObject {
		if st == http.StatusTooManyRequests {
			return RaiseSos429ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}
		}
		return RaiseSosdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil || !req.Body.Type.Valid() {
		return fail(invalid()), nil
	}
	v, err := s.Service.RaiseSos(ctx, principal(ctx), req.Params.IdempotencyKey, string(req.Body.Type), req.Body.Text)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return fail(st, c, t), nil
		}
		return nil, err
	}
	return RaiseSos201JSONResponse(toAPISos(v)), nil
}

// CloseSos closes the caller's SOS (idempotent).
func (s *Server) CloseSos(ctx context.Context, req CloseSosRequestObject) (CloseSosResponseObject, error) {
	v, err := s.Service.CloseSos(ctx, principal(ctx), req.SosId)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return CloseSosdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return CloseSos200JSONResponse(toAPISos(v)), nil
}

// ListSosResponses returns the responders to the caller's SOS.
func (s *Server) ListSosResponses(ctx context.Context, req ListSosResponsesRequestObject) (ListSosResponsesResponseObject, error) {
	list, err := s.Service.ListResponses(ctx, principal(ctx), req.SosId)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListSosResponsesdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(ListSosResponses200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, SosResponse{
			Id: r.ID, MemberId: r.ResponderID, DisplayName: r.DisplayName, MetInPerson: r.MetInPerson, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// AcceptSosResponse chooses a responder; chat opens the room later.
func (s *Server) AcceptSosResponse(ctx context.Context, req AcceptSosResponseRequestObject) (AcceptSosResponseResponseObject, error) {
	if err := s.Service.AcceptResponse(ctx, principal(ctx), req.SosId, req.ResponseId); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return AcceptSosResponsedefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return AcceptSosResponse202Response{}, nil
}

// ShareSosToDistrict shares the caller's SOS to her district feed; 403 when
// she is not verified, 409 for childcare or a closed SOS.
func (s *Server) ShareSosToDistrict(ctx context.Context, req ShareSosToDistrictRequestObject) (ShareSosToDistrictResponseObject, error) {
	v, err := s.Service.ShareSos(ctx, principal(ctx), req.SosId)
	if err != nil {
		st, c, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		switch st {
		case http.StatusForbidden:
			return ShareSosToDistrict403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		case http.StatusConflict:
			return ShareSosToDistrict409ApplicationProblemPlusJSONResponse(problem(st, c, t)), nil
		}
		return ShareSosToDistrictdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	return ShareSosToDistrict200JSONResponse(toAPISos(v)), nil
}

// AddTrustedContact adds an external contact or invites a member; 409 at
// the limit or for a duplicate.
func (s *Server) AddTrustedContact(ctx context.Context, req AddTrustedContactRequestObject) (AddTrustedContactResponseObject, error) {
	fail := func(st int, c, t string) AddTrustedContactResponseObject {
		if st == http.StatusConflict {
			return AddTrustedContact409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}
		}
		return AddTrustedContactdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}
	}
	if req.Body == nil || !req.Body.Kind.Valid() {
		return fail(invalid()), nil
	}
	in := model.NewTrustedContact{Kind: string(req.Body.Kind)}
	switch req.Body.Kind {
	case TrustedContactInputKindMember:
		if req.Body.MemberId == nil || req.Body.Name != nil || req.Body.Phone != nil {
			return fail(invalid()), nil
		}
		in.MemberID = *req.Body.MemberId
	case TrustedContactInputKindExternal:
		if req.Body.MemberId != nil || req.Body.Name == nil || req.Body.Phone == nil {
			return fail(invalid()), nil
		}
		in.Name, in.Phone = *req.Body.Name, *req.Body.Phone
	}
	c, err := s.Service.AddTrustedContact(ctx, principal(ctx), req.Params.IdempotencyKey, in)
	if err != nil {
		if st, code, t, ok := problemFor(err); ok {
			return fail(st, code, t), nil
		}
		return nil, err
	}
	return AddTrustedContact201JSONResponse(toAPIContact(c)), nil
}

// RemoveTrustedContact removes one of the caller's contacts (idempotent).
func (s *Server) RemoveTrustedContact(ctx context.Context, req RemoveTrustedContactRequestObject) (RemoveTrustedContactResponseObject, error) {
	if err := s.Service.RemoveTrustedContact(ctx, principal(ctx), req.ContactId); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return RemoveTrustedContactdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return RemoveTrustedContact204Response{}, nil
}

// AcceptTrustedInvitation confirms an invitation addressed to the caller.
func (s *Server) AcceptTrustedInvitation(ctx context.Context, req AcceptTrustedInvitationRequestObject) (AcceptTrustedInvitationResponseObject, error) {
	if err := s.Service.AcceptTrustedInvitation(ctx, principal(ctx), req.ContactId); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return AcceptTrustedInvitationdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return AcceptTrustedInvitation204Response{}, nil
}

// GetSosFeed returns the caller's district feed; 403 when not verified.
func (s *Server) GetSosFeed(ctx context.Context, req GetSosFeedRequestObject) (GetSosFeedResponseObject, error) {
	var cursor string
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	page, err := s.Service.Feed(ctx, principal(ctx), cursor)
	if err != nil {
		st, c, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		if st == http.StatusForbidden {
			return GetSosFeed403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		}
		return GetSosFeeddefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	out := GetSosFeed200JSONResponse{Items: make([]SosFeedItem, 0, len(page.Items))}
	for _, it := range page.Items {
		out.Items = append(out.Items, SosFeedItem{
			Id: it.ID, Type: SosFeedItemType(it.Type), Text: it.Text, DisplayName: it.DisplayName,
			CreatedAt: it.CreatedAt, RespondedByMe: it.RespondedByMe,
		})
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out, nil
}

// RespondToSos records "I can help"; 403 when not verified.
func (s *Server) RespondToSos(ctx context.Context, req RespondToSosRequestObject) (RespondToSosResponseObject, error) {
	if err := s.Service.RespondToSos(ctx, principal(ctx), req.SosId); err != nil {
		st, c, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		if st == http.StatusForbidden {
			return RespondToSos403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		}
		return RespondToSosdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	return RespondToSos204Response{}, nil
}

// GetSharedPlan returns another member's child card to her confirmed trusted
// contact after she sent it; audited, Cache-Control: no-store.
func (s *Server) GetSharedPlan(ctx context.Context, req GetSharedPlanRequestObject) (GetSharedPlanResponseObject, error) {
	plan, err := s.Service.GetSharedPlan(ctx, principal(ctx), req.OwnerId)
	if err != nil {
		st, c, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		if st == http.StatusForbidden {
			return GetSharedPlan403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		}
		return GetSharedPlandefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	cc := noStore
	return GetSharedPlan200JSONResponse{
		Body:    SharedPlan{OwnerDisplayName: plan.OwnerDisplayName, ChildCard: plan.ChildCard, SentAt: plan.SentAt},
		Headers: GetSharedPlan200ResponseHeaders{CacheControl: &cc},
	}, nil
}

// ViewExternalPlan serves the child card behind an SMS link (security: [],
// no session). Every failure of the token is the same 404 problem, so a
// token cannot be probed; the per-IP limit (429) sits in front of this
// handler in the wiring. The token is never echoed, logged or put into an
// error. Cache-Control: no-store.
func (s *Server) ViewExternalPlan(ctx context.Context, req ViewExternalPlanRequestObject) (ViewExternalPlanResponseObject, error) {
	notFound := func() ViewExternalPlanResponseObject {
		st, c, t, _ := problemFor(model.ErrNotFound)
		return ViewExternalPlan404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}
	}
	if req.Body == nil {
		st, c, t := invalid()
		return ViewExternalPlandefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	plan, err := s.Service.ViewExternalPlan(ctx, req.Body.Token)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return notFound(), nil
		}
		return nil, err
	}
	cc := noStore
	return ViewExternalPlan200JSONResponse{
		Body:    SharedPlan{OwnerDisplayName: plan.OwnerDisplayName, ChildCard: plan.ChildCard, SentAt: plan.SentAt},
		Headers: ViewExternalPlan200ResponseHeaders{CacheControl: &cc},
	}, nil
}

// ViewExternalSos serves the SOS behind the link in an external trusted
// contact's SOS SMS (security: [], no session). Every failure of the token
// (unknown, expired, revoked, SOS closed, contact removed) is the same 404
// problem, so a token cannot be probed; the per-IP limit (429) sits in front
// of this handler in the wiring. The token is never echoed, logged or put
// into an error. Cache-Control: no-store.
func (s *Server) ViewExternalSos(ctx context.Context, req ViewExternalSosRequestObject) (ViewExternalSosResponseObject, error) {
	if req.Body == nil {
		st, c, t := invalid()
		return ViewExternalSosdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	v, err := s.Service.ViewExternalSos(ctx, req.Body.Token)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			st, c, t, _ := problemFor(model.ErrNotFound)
			return ViewExternalSos404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		}
		return nil, err
	}
	closes := v.ClosesAt
	cc := noStore
	return ViewExternalSos200JSONResponse{
		Body: ExternalSos{
			OwnerDisplayName: v.OwnerDisplayName, Type: SosType(v.Type), Text: v.Text,
			Status: ExternalSosStatus(v.Status), CreatedAt: v.CreatedAt, ClosesAt: &closes,
		},
		Headers: ViewExternalSos200ResponseHeaders{CacheControl: &cc},
	}, nil
}
