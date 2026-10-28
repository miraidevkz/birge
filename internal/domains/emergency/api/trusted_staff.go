package api

import (
	"context"
	"net/http"

	"birge/internal/domains/emergency/model"
)

// GetTrustedSos returns an SOS to a confirmed trusted contact of its author
// (any type, childcare included); 403 for anyone else, 404 for an unknown
// SOS. Audited by the service; Cache-Control: no-store.
func (s *Server) GetTrustedSos(ctx context.Context, req GetTrustedSosRequestObject) (GetTrustedSosResponseObject, error) {
	v, err := s.Service.GetTrustedSos(ctx, principal(ctx), req.SosId)
	if err != nil {
		st, c, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		if st == http.StatusForbidden {
			return GetTrustedSos403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		}
		return GetTrustedSosdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	closes := v.ClosesAt
	cc := noStore
	return GetTrustedSos200JSONResponse{
		Body: TrustedSos{
			Id: v.ID, OwnerDisplayName: v.OwnerDisplayName, Type: SosType(v.Type), Text: v.PlainText,
			Status: TrustedSosStatus(v.Status), CreatedAt: v.CreatedAt, ClosesAt: &closes, RespondedByMe: v.RespondedByMe,
		},
		Headers: GetTrustedSos200ResponseHeaders{CacheControl: &cc},
	}, nil
}

// RespondAsTrustedContact records "I can help" from a confirmed trusted
// contact; 403 for anyone else, 409 on a closed SOS.
func (s *Server) RespondAsTrustedContact(ctx context.Context, req RespondAsTrustedContactRequestObject) (RespondAsTrustedContactResponseObject, error) {
	if err := s.Service.RespondAsTrustedContact(ctx, principal(ctx), req.SosId); err != nil {
		st, c, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		switch st {
		case http.StatusForbidden:
			return RespondAsTrustedContact403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		case http.StatusConflict:
			return RespondAsTrustedContact409ApplicationProblemPlusJSONResponse(problem(st, c, t)), nil
		}
		return RespondAsTrustedContactdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	return RespondAsTrustedContact204Response{}, nil
}

// GetTrustedInvitation returns an invitation addressed to the caller; 404
// otherwise.
func (s *Server) GetTrustedInvitation(ctx context.Context, req GetTrustedInvitationRequestObject) (GetTrustedInvitationResponseObject, error) {
	v, err := s.Service.GetTrustedInvitation(ctx, principal(ctx), req.ContactId)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return GetTrustedInvitationdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return GetTrustedInvitation200JSONResponse{
		ContactId: v.ContactID, OwnerDisplayName: v.OwnerDisplayName,
		Status: TrustedInvitationStatus(v.Status), InvitedAt: v.InvitedAt,
	}, nil
}

// DeclineTrustedInvitation declines an invitation or ends being a trusted
// contact (idempotent); 404 when it is not addressed to the caller.
func (s *Server) DeclineTrustedInvitation(ctx context.Context, req DeclineTrustedInvitationRequestObject) (DeclineTrustedInvitationResponseObject, error) {
	if err := s.Service.DeclineTrustedInvitation(ctx, principal(ctx), req.ContactId); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return DeclineTrustedInvitationdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return DeclineTrustedInvitation204Response{}, nil
}

func toAPIStaffSos(v model.StaffSosView) StaffSos {
	closes := v.ClosesAt
	return StaffSos{
		Id: v.ID, MemberId: v.MemberID, DisplayName: v.DisplayName, DistrictId: v.MemberDistrictID,
		Type: SosType(v.Type), Text: v.PlainText, Status: StaffSosStatus(v.Status), SharedToDistrict: v.Shared(),
		CreatedAt: v.CreatedAt, ClosesAt: &closes, ClosedAt: v.ClosedAt, SeenAt: v.SeenAt, SeenBy: v.SeenBy,
		ResponseCount: v.ResponseCount,
	}
}

// ListStaffSos returns one page of the moderator SOS queue (moderator or
// admin with MFA; 403 otherwise). Every item is audited by the service;
// Cache-Control: no-store.
func (s *Server) ListStaffSos(ctx context.Context, req ListStaffSosRequestObject) (ListStaffSosResponseObject, error) {
	var status, cursor string
	if req.Params.Status != nil {
		if !req.Params.Status.Valid() {
			st, c, t := invalid()
			return ListStaffSosdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		status = string(*req.Params.Status)
	}
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	page, err := s.Service.ListStaffSos(ctx, principal(ctx), status, cursor)
	if err != nil {
		st, c, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		if st == http.StatusForbidden {
			return ListStaffSos403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		}
		return ListStaffSosdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	body := StaffSosPage{Items: make([]StaffSos, 0, len(page.Items))}
	for _, it := range page.Items {
		body.Items = append(body.Items, toAPIStaffSos(it))
	}
	if page.NextCursor != "" {
		body.NextCursor = &page.NextCursor
	}
	cc := noStore
	return ListStaffSos200JSONResponse{Body: body, Headers: ListStaffSos200ResponseHeaders{CacheControl: &cc}}, nil
}

// MarkSosSeen records a moderator's "Seen" (idempotent; the first is kept).
func (s *Server) MarkSosSeen(ctx context.Context, req MarkSosSeenRequestObject) (MarkSosSeenResponseObject, error) {
	v, err := s.Service.MarkSosSeen(ctx, principal(ctx), req.SosId)
	if err != nil {
		st, c, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		if st == http.StatusForbidden {
			return MarkSosSeen403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		}
		return MarkSosSeendefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	return MarkSosSeen200JSONResponse(toAPIStaffSos(v)), nil
}

// CloseSosAsStaff closes an SOS on a moderator's "Close" (idempotent).
func (s *Server) CloseSosAsStaff(ctx context.Context, req CloseSosAsStaffRequestObject) (CloseSosAsStaffResponseObject, error) {
	v, err := s.Service.CloseSosAsStaff(ctx, principal(ctx), req.SosId)
	if err != nil {
		st, c, t, ok := problemFor(err)
		if !ok {
			return nil, err
		}
		if st == http.StatusForbidden {
			return CloseSosAsStaff403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		}
		return CloseSosAsStaffdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	return CloseSosAsStaff200JSONResponse(toAPIStaffSos(v)), nil
}
