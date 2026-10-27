// Package api exposes the wellbeing HTTP handlers (OpenAPI tag wellbeing).
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"birge/internal/domains/wellbeing/model"
	"birge/internal/domains/wellbeing/service"
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

// GetCheckinSchedule returns the caller's reminder schedule (empty fields when unset).
func (s *Server) GetCheckinSchedule(ctx context.Context, _ GetCheckinScheduleRequestObject) (GetCheckinScheduleResponseObject, error) {
	mid, ok := memberID(ctx)
	if !ok {
		return scheduleGetProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	sched, err := s.Service.GetSchedule(ctx, mid)
	if err != nil {
		return nil, err
	}
	return GetCheckinSchedule200JSONResponse(toAPISchedule(sched)), nil
}

// SetCheckinSchedule stores the reminder preference and (re)schedules the timer.
func (s *Server) SetCheckinSchedule(ctx context.Context, request SetCheckinScheduleRequestObject) (SetCheckinScheduleResponseObject, error) {
	mid, ok := memberID(ctx)
	if !ok {
		return scheduleSetProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	if request.Body == nil || request.Body.Weekday == nil || request.Body.Hour == nil {
		return scheduleSetProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	enabled := true
	if request.Body.Enabled != nil {
		enabled = *request.Body.Enabled
	}
	sched, err := s.Service.SetSchedule(ctx, mid, *request.Body.Weekday, *request.Body.Hour, enabled)
	if err != nil {
		if errors.Is(err, model.ErrInvalidInput) {
			return scheduleSetProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
		}
		return nil, err
	}
	return SetCheckinSchedule200JSONResponse(toAPISchedule(sched)), nil
}

// GetQuestionnaire returns the configured questionnaire (locale keys).
func (s *Server) GetQuestionnaire(ctx context.Context, _ GetQuestionnaireRequestObject) (GetQuestionnaireResponseObject, error) {
	mid, ok := memberID(ctx)
	if !ok {
		return questionnaireProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	q, err := s.Service.GetQuestionnaire(ctx, mid)
	if err != nil {
		if errors.Is(err, model.ErrInvalidInput) {
			return questionnaireProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
		}
		return nil, err
	}
	return GetQuestionnaire200JSONResponse(toAPIQuestionnaire(q)), nil
}

// SubmitCheckin scores and stores a weekly check-in.
func (s *Server) SubmitCheckin(ctx context.Context, request SubmitCheckinRequestObject) (SubmitCheckinResponseObject, error) {
	mid, ok := memberID(ctx)
	if !ok {
		return submitProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	if request.Body == nil {
		return submitProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	answers := make([]model.Answer, 0, len(request.Body.Answers))
	for _, a := range request.Body.Answers {
		answers = append(answers, model.Answer{QuestionID: a.QuestionId, Value: a.Value})
	}
	result, err := s.Service.SubmitCheckin(ctx, mid, request.Params.IdempotencyKey, request.Body.QuestionnaireVersion, answers)
	if err != nil {
		if errors.Is(err, model.ErrInvalidInput) {
			return submitProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
		}
		if errors.Is(err, model.ErrUnauthorized) {
			return submitProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
		}
		return nil, err
	}
	return SubmitCheckin201JSONResponse(toAPIResult(result)), nil
}

// GetCheckinHistory returns chart points for the last N weeks.
func (s *Server) GetCheckinHistory(ctx context.Context, request GetCheckinHistoryRequestObject) (GetCheckinHistoryResponseObject, error) {
	mid, ok := memberID(ctx)
	if !ok {
		return historyProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	weeks := 0
	if request.Params.Weeks != nil {
		weeks = *request.Params.Weeks
	}
	points, err := s.Service.GetHistory(ctx, mid, weeks)
	if err != nil {
		return nil, err
	}
	out := make(GetCheckinHistory200JSONResponse, 0, len(points))
	for _, p := range points {
		out = append(out, CheckinPoint{
			CompletedAt: p.CompletedAt,
			Phq4:        p.PHQ4,
			Pss4:        p.PSS4,
		})
	}
	return out, nil
}

// ListPractices returns audio practices for the requested language.
func (s *Server) ListPractices(ctx context.Context, request ListPracticesRequestObject) (ListPracticesResponseObject, error) {
	mid, ok := memberID(ctx)
	if !ok {
		return practicesProblem(http.StatusUnauthorized, "unauthorized", "Sign in required."), nil
	}
	if !request.Params.Lang.Valid() {
		return practicesProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
	}
	items, err := s.Service.ListPractices(ctx, mid, string(request.Params.Lang))
	if err != nil {
		if errors.Is(err, model.ErrInvalidInput) {
			return practicesProblem(http.StatusBadRequest, "invalid_request", "The request is invalid."), nil
		}
		return nil, err
	}
	out := make(ListPractices200JSONResponse, 0, len(items))
	for _, p := range items {
		out = append(out, Practice{
			Id:              p.ID,
			TitleKey:        p.TitleKey,
			DurationSeconds: p.DurationSeconds,
			Url:             p.URL,
		})
	}
	return out, nil
}

func toAPISchedule(s model.Schedule) CheckinSchedule {
	if s.MemberID == uuid.Nil {
		return CheckinSchedule{}
	}
	wd, hour, enabled := s.Weekday, s.Hour, s.Enabled
	return CheckinSchedule{Weekday: &wd, Hour: &hour, Enabled: &enabled}
}

func toAPIQuestionnaire(q model.Questionnaire) Questionnaire {
	questions := make([]struct {
		Id         string                           `json:"id"`
		Instrument QuestionnaireQuestionsInstrument `json:"instrument"`
		Options    []struct {
			TextKey string `json:"textKey"`
			Value   int    `json:"value"`
		} `json:"options"`
		TextKey string `json:"textKey"`
	}, 0, len(q.Questions))
	for _, qu := range q.Questions {
		opts := make([]struct {
			TextKey string `json:"textKey"`
			Value   int    `json:"value"`
		}, 0, len(qu.Options))
		for _, o := range qu.Options {
			opts = append(opts, struct {
				TextKey string `json:"textKey"`
				Value   int    `json:"value"`
			}{TextKey: o.TextKey, Value: o.Value})
		}
		questions = append(questions, struct {
			Id         string                           `json:"id"`
			Instrument QuestionnaireQuestionsInstrument `json:"instrument"`
			Options    []struct {
				TextKey string `json:"textKey"`
				Value   int    `json:"value"`
			} `json:"options"`
			TextKey string `json:"textKey"`
		}{
			Id:         qu.ID,
			Instrument: QuestionnaireQuestionsInstrument(qu.Instrument),
			Options:    opts,
			TextKey:    qu.TextKey,
		})
	}
	return Questionnaire{Version: q.Version, Questions: questions}
}

func toAPIResult(r model.CheckinResult) CheckinResult {
	out := CheckinResult{
		Id:          r.ID,
		Flag:        CheckinFlag(r.Flag),
		FeedbackKey: r.FeedbackKey,
		Suggestion:  CheckinResultSuggestion(r.Suggestion),
	}
	if r.EscalationID != nil {
		id := *r.EscalationID
		out.EscalationId = &id
	}
	if len(r.CrisisPhones) > 0 {
		phones := make([]CrisisPhone, 0, len(r.CrisisPhones))
		for _, p := range r.CrisisPhones {
			phones = append(phones, CrisisPhone{Label: p.Label, Number: p.Number})
		}
		out.CrisisPhones = &phones
	}
	return out
}

func scheduleGetProblem(status int, code, title string) GetCheckinScheduledefaultApplicationProblemPlusJSONResponse {
	return GetCheckinScheduledefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func scheduleSetProblem(status int, code, title string) SetCheckinScheduledefaultApplicationProblemPlusJSONResponse {
	return SetCheckinScheduledefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func questionnaireProblem(status int, code, title string) GetQuestionnairedefaultApplicationProblemPlusJSONResponse {
	return GetQuestionnairedefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func submitProblem(status int, code, title string) SubmitCheckindefaultApplicationProblemPlusJSONResponse {
	return SubmitCheckindefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func historyProblem(status int, code, title string) GetCheckinHistorydefaultApplicationProblemPlusJSONResponse {
	return GetCheckinHistorydefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

func practicesProblem(status int, code, title string) ListPracticesdefaultApplicationProblemPlusJSONResponse {
	return ListPracticesdefaultApplicationProblemPlusJSONResponse{
		StatusCode: status,
		Body:       Problem{Type: "about:blank", Title: title, Status: status, Code: &code},
	}
}

var _ StrictServerInterface = (*Server)(nil)
