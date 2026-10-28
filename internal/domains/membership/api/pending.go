package api

import (
	"context"
	"net/http"
)

// StaffListMembers lists verified or suspended members for staff. The
// contract landed before the implementation (ADR-0005): until it is
// implemented (docs/roadmap.md) it answers 501, and the admin app
// shows that the list is not available yet.
func (s *Server) StaffListMembers(_ context.Context, _ StaffListMembersRequestObject) (StaffListMembersResponseObject, error) {
	return StaffListMembersdefaultApplicationProblemPlusJSONResponse{
		StatusCode: http.StatusNotImplemented,
		Body:       problem(http.StatusNotImplemented, "not_implemented", "Not implemented yet"),
	}, nil
}

// GrantMentorRole gives a verified member the mentor role. Answers 501 until
// it is implemented (docs/roadmap.md); the admin Rooms picker says so.
func (s *Server) GrantMentorRole(_ context.Context, _ GrantMentorRoleRequestObject) (GrantMentorRoleResponseObject, error) {
	return GrantMentorRoledefaultApplicationProblemPlusJSONResponse{
		StatusCode: http.StatusNotImplemented,
		Body:       problem(http.StatusNotImplemented, "not_implemented", "Not implemented yet"),
	}, nil
}

// RevokeMentorRole takes the mentor role away. Answers 501 until it is
// implemented (docs/roadmap.md).
func (s *Server) RevokeMentorRole(_ context.Context, _ RevokeMentorRoleRequestObject) (RevokeMentorRoleResponseObject, error) {
	return RevokeMentorRoledefaultApplicationProblemPlusJSONResponse{
		StatusCode: http.StatusNotImplemented,
		Body:       problem(http.StatusNotImplemented, "not_implemented", "Not implemented yet"),
	}, nil
}
