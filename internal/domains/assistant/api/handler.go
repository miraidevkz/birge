// Package api exposes the assistant HTTP handlers (OpenAPI tag assistant).
// Only member sessions are served; staff principals are refused.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"birge/internal/domains/assistant/model"
	"birge/internal/domains/assistant/service"
	"birge/internal/platform/auth"
)

// Server implements the generated StrictServerInterface.
type Server struct {
	Service *service.Service
}

func memberID(ctx context.Context) (uuid.UUID, bool) {
	p, ok := auth.FromContext(ctx)
	if !ok || p.Staff || p.MemberID == uuid.Nil {
		return uuid.Nil, false
	}
	return p.MemberID, true
}

// SendAssistantMessage runs the assistant pipeline for one message. Provider
// failures come back as 200 with degraded=true, never as a 5xx.
func (s *Server) SendAssistantMessage(ctx context.Context, request SendAssistantMessageRequestObject) (SendAssistantMessageResponseObject, error) {
	mid, ok := memberID(ctx)
	if !ok {
		return sendProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	if request.Body == nil || !request.Body.Scope.Valid() || (request.Body.Lang != nil && !request.Body.Lang.Valid()) {
		return sendProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	in := service.MessageInput{Text: request.Body.Text, Scope: string(request.Body.Scope)}
	if request.Body.Lang != nil {
		in.Lang = string(*request.Body.Lang)
	}
	reply, err := s.Service.SendMessage(ctx, mid, request.Params.IdempotencyKey, in)
	switch {
	case err == nil:
		return SendAssistantMessage200JSONResponse(toAPIReply(reply)), nil
	case errors.Is(err, model.ErrConsentRequired):
		code := "consent_required"
		return SendAssistantMessage403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse{
			Type: "about:blank", Title: "The AI consent is required.", Status: http.StatusForbidden, Code: &code,
		}}, nil
	case errors.Is(err, model.ErrDailyLimit):
		code := "daily_limit"
		return SendAssistantMessage429ApplicationProblemPlusJSONResponse{
			Type: "about:blank", Title: "The daily message limit is reached.", Status: http.StatusTooManyRequests, Code: &code,
		}, nil
	case errors.Is(err, model.ErrInvalidInput):
		return sendProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	case errors.Is(err, model.ErrUnauthorized):
		return sendProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	return nil, err
}

// RateAssistantReply records "Helped / Did not help" for the caller's own reply.
func (s *Server) RateAssistantReply(ctx context.Context, request RateAssistantReplyRequestObject) (RateAssistantReplyResponseObject, error) {
	mid, ok := memberID(ctx)
	if !ok {
		return rateProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	if request.Body == nil {
		return rateProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	err := s.Service.RateReply(ctx, mid, request.MessageId, request.Body.Helped)
	switch {
	case err == nil:
		return RateAssistantReply204Response{}, nil
	case errors.Is(err, model.ErrNotFound):
		return rateProblem(http.StatusNotFound, "not_found", "The message was not found."), nil
	case errors.Is(err, model.ErrUnauthorized):
		return rateProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	return nil, err
}

func toAPIReply(r model.Reply) AssistantReply {
	out := AssistantReply{
		MessageId:    r.MessageID,
		Text:         r.Text,
		Crisis:       r.Crisis,
		EscalationId: r.EscalationID,
		Degraded:     r.Degraded,
		Sources:      make([]ArticleRef, 0, len(r.Sources)),
		Suggest:      AssistantSuggestion(r.Suggest),
	}
	if r.Crisis {
		out.Text = nil // the client shows its fixed locale text
	}
	for _, src := range r.Sources {
		ref := ArticleRef{ArticleId: src.ArticleID, Title: src.Title}
		if src.SourceURL != "" {
			u := src.SourceURL
			ref.SourceUrl = &u
		}
		out.Sources = append(out.Sources, ref)
	}
	if len(r.CrisisPhones) > 0 {
		phones := make([]CrisisPhone, 0, len(r.CrisisPhones))
		for _, p := range r.CrisisPhones {
			phones = append(phones, CrisisPhone{Number: p.Number, Label: p.Label})
		}
		out.CrisisPhones = &phones
	}
	if r.DisclaimerKey != "" {
		k := r.DisclaimerKey
		out.DisclaimerKey = &k
	}
	if r.RemainingToday >= 0 {
		remaining := r.RemainingToday
		out.RemainingToday = &remaining
	}
	return out
}

func sendProblem(status int, code, title string) SendAssistantMessagedefaultApplicationProblemPlusJSONResponse {
	return SendAssistantMessagedefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func rateProblem(status int, code, title string) RateAssistantReplydefaultApplicationProblemPlusJSONResponse {
	return RateAssistantReplydefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

var _ StrictServerInterface = (*Server)(nil)
