package api

import (
	"context"
	"errors"
	"net/http"

	"birge/internal/domains/privacy/model"
	"birge/internal/domains/privacy/service"
	"birge/internal/platform/auth"
)

// Server implements the generated StrictServerInterface.
type Server struct {
	Service *service.Service
}

// ListConsents returns the caller's current consent states.
func (s *Server) ListConsents(ctx context.Context, _ ListConsentsRequestObject) (ListConsentsResponseObject, error) {
	p, ok := auth.FromContext(ctx)
	if !ok || p.Staff {
		return listConsentsProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	states, err := s.Service.ListConsents(ctx, p.IdentityID)
	if err != nil {
		return nil, err
	}
	items := make([]Consent, 0, len(states))
	for _, st := range states {
		items = append(items, toAPIConsent(st))
	}
	return ListConsents200JSONResponse(items), nil
}

// SetConsent grants or revokes a consent for the current text version.
func (s *Server) SetConsent(ctx context.Context, request SetConsentRequestObject) (SetConsentResponseObject, error) {
	p, ok := auth.FromContext(ctx)
	if !ok || p.Staff {
		return setConsentProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	if request.Body == nil {
		return setConsentProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	body := request.Body
	if !body.Type.Valid() {
		return setConsentProblem(http.StatusBadRequest, "invalid_request", "Unknown consent type."), nil
	}
	state, err := s.Service.SetConsent(ctx, p.IdentityID, string(body.Type), body.Version, body.Granted)
	if err != nil {
		if errors.Is(err, model.ErrInvalidInput) {
			return setConsentProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
		}
		return nil, err
	}
	return SetConsent200JSONResponse(toAPIConsent(state)), nil
}

// RequestDeletion schedules deletion of all the member's data within 24 hours.
func (s *Server) RequestDeletion(ctx context.Context, request RequestDeletionRequestObject) (RequestDeletionResponseObject, error) {
	p, ok := auth.FromContext(ctx)
	if !ok || p.Staff {
		return requestDeletionProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	if request.Body == nil || request.Body.Confirm != DELETE {
		return requestDeletionProblem(http.StatusBadRequest, "invalid_request", "Confirmation required."), nil
	}
	req, err := s.Service.RequestDeletion(ctx, p.IdentityID)
	if err != nil {
		if errors.Is(err, model.ErrInvalidInput) {
			return requestDeletionProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
		}
		return nil, err
	}
	return RequestDeletion202JSONResponse{
		RequestedAt: req.RequestedAt,
		DueAt:       req.DueAt,
	}, nil
}

func toAPIConsent(st model.ConsentState) Consent {
	out := Consent{
		Type:           ConsentType(st.Type),
		Granted:        st.Granted,
		CurrentVersion: st.CurrentVersion,
		GrantedVersion: st.GrantedVersion,
		ChangedAt:      st.ChangedAt,
	}
	return out
}

func listConsentsProblem(status int, code, title string) ListConsentsdefaultApplicationProblemPlusJSONResponse {
	return ListConsentsdefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func setConsentProblem(status int, code, title string) SetConsentdefaultApplicationProblemPlusJSONResponse {
	return SetConsentdefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func requestDeletionProblem(status int, code, title string) RequestDeletiondefaultApplicationProblemPlusJSONResponse {
	return RequestDeletiondefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

var _ StrictServerInterface = (*Server)(nil)
