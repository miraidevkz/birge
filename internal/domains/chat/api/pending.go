package api

import (
	"context"
	"net/http"
)

// StaffListRooms lists group rooms for mentor assignment (staff). The
// contract landed before the implementation (ADR-0005): until it is
// implemented (docs/roadmap.md) it answers 501 so the admin app can show a
// clear "not available yet" state instead of a broken list.
func (s *Server) StaffListRooms(_ context.Context, _ StaffListRoomsRequestObject) (StaffListRoomsResponseObject, error) {
	return StaffListRoomsdefaultApplicationProblemPlusJSONResponse{
		StatusCode: http.StatusNotImplemented,
		Body:       problem(http.StatusNotImplemented, "not_implemented", "Not implemented yet"),
	}, nil
}
