package api

import (
	"context"
	"net/http"
)

// The operations below landed in the contract before their implementation
// (ADR-0005). Until they are implemented (docs/roadmap.md) they answer 501
// not_implemented, which the clients show as "not available on this server
// yet".

func notImplemented() Problem {
	return problem(http.StatusNotImplemented, "not_implemented", "Not implemented yet")
}

// GetTrustedSos is a trusted contact's view of an SOS; 501 until implemented.
func (s *Server) GetTrustedSos(_ context.Context, _ GetTrustedSosRequestObject) (GetTrustedSosResponseObject, error) {
	return GetTrustedSosdefaultApplicationProblemPlusJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
}

// RespondAsTrustedContact is a trusted contact's "I can help"; 501 until implemented.
func (s *Server) RespondAsTrustedContact(_ context.Context, _ RespondAsTrustedContactRequestObject) (RespondAsTrustedContactResponseObject, error) {
	return RespondAsTrustedContactdefaultApplicationProblemPlusJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
}

// GetTrustedInvitation is reading a trusted invitation; 501 until implemented.
func (s *Server) GetTrustedInvitation(_ context.Context, _ GetTrustedInvitationRequestObject) (GetTrustedInvitationResponseObject, error) {
	return GetTrustedInvitationdefaultApplicationProblemPlusJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
}

// DeclineTrustedInvitation is declining a trusted invitation; 501 until implemented.
func (s *Server) DeclineTrustedInvitation(_ context.Context, _ DeclineTrustedInvitationRequestObject) (DeclineTrustedInvitationResponseObject, error) {
	return DeclineTrustedInvitationdefaultApplicationProblemPlusJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
}

// ListStaffSos is the moderators' SOS queue; 501 until implemented.
func (s *Server) ListStaffSos(_ context.Context, _ ListStaffSosRequestObject) (ListStaffSosResponseObject, error) {
	return ListStaffSosdefaultApplicationProblemPlusJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
}

// MarkSosSeen is a moderator's "Seen"; 501 until implemented.
func (s *Server) MarkSosSeen(_ context.Context, _ MarkSosSeenRequestObject) (MarkSosSeenResponseObject, error) {
	return MarkSosSeendefaultApplicationProblemPlusJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
}

// CloseSosAsStaff is a moderator's "Close"; 501 until implemented.
func (s *Server) CloseSosAsStaff(_ context.Context, _ CloseSosAsStaffRequestObject) (CloseSosAsStaffResponseObject, error) {
	return CloseSosAsStaffdefaultApplicationProblemPlusJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
}
