package api

import (
	"context"
	"net/http"
)

// ViewExternalPlan serves the child card behind an SMS link (no session).
// The contract landed before the implementation (ADR-0005): until the brief
// w25-emergency-external-plan-link is done it answers 501, and
// sendEmergencyPlan keeps sending the SMS without a working link.
func (s *Server) ViewExternalPlan(_ context.Context, _ ViewExternalPlanRequestObject) (ViewExternalPlanResponseObject, error) {
	return ViewExternalPlandefaultApplicationProblemPlusJSONResponse{
		StatusCode: http.StatusNotImplemented,
		Body:       problem(http.StatusNotImplemented, "not_implemented", "Not implemented yet"),
	}, nil
}
