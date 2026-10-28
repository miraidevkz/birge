// Package api exposes the analytics HTTP handlers (OpenAPI tag analytics):
// the pilot metrics as JSON and as CSV. Authorization, the period rule,
// suppression and audit are enforced in the service; handlers map errors to
// problem responses and shape the result.
package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/service"
	"birge/internal/platform/auth"
)

// Server implements the generated StrictServerInterface.
type Server struct {
	Service *service.Service
}

var _ StrictServerInterface = (*Server)(nil)

// noStore keeps reports out of every cache (contract Cache-Control).
var noStore = "no-store"

func principal(ctx context.Context) auth.Principal {
	p, _ := auth.FromContext(ctx)
	return p
}

func problemFor(err error) (status int, code, title string, ok bool) {
	switch {
	case errors.Is(err, model.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized", "Sign in required.", true
	case errors.Is(err, model.ErrForbidden):
		return http.StatusForbidden, "forbidden", "Not allowed.", true
	case errors.Is(err, model.ErrInvalidInput):
		return http.StatusBadRequest, "invalid_period", "The period is invalid: to must not be before from, and at most 12 months after it.", true
	}
	return 0, "", "", false
}

func problem(status int, code, title string) Problem {
	return Problem{Type: "about:blank", Title: title, Status: status, Code: &code}
}

// GetMetrics returns the suppressed metrics for the period.
func (s *Server) GetMetrics(ctx context.Context, req GetMetricsRequestObject) (GetMetricsResponseObject, error) {
	m, err := s.Service.Metrics(ctx, principal(ctx), req.Params.From.Time, req.Params.To.Time)
	if err != nil {
		st, c, t, ok := problemFor(err)
		switch {
		case !ok:
			return nil, err
		case st == http.StatusBadRequest:
			return GetMetrics400ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		case st == http.StatusForbidden:
			return GetMetrics403ApplicationProblemPlusJSONResponse(problem(st, c, t)), nil
		default:
			return GetMetricsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
	}
	return GetMetrics200JSONResponse{Body: toAPI(m), Headers: GetMetrics200ResponseHeaders{CacheControl: &noStore}}, nil
}

// GetMetricsCsv returns the same metrics as a CSV attachment.
func (s *Server) GetMetricsCsv(ctx context.Context, req GetMetricsCsvRequestObject) (GetMetricsCsvResponseObject, error) {
	body, name, err := s.Service.MetricsCSV(ctx, principal(ctx), req.Params.From.Time, req.Params.To.Time)
	if err != nil {
		st, c, t, ok := problemFor(err)
		switch {
		case !ok:
			return nil, err
		case st == http.StatusBadRequest:
			return GetMetricsCsv400ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
		case st == http.StatusForbidden:
			return GetMetricsCsv403ApplicationProblemPlusJSONResponse(problem(st, c, t)), nil
		default:
			return GetMetricsCsvdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
	}
	// The name is built from two validated dates only, so it needs no escaping.
	disposition := `attachment; filename="` + name + `"`
	return GetMetricsCsv200TextcsvResponse{
		Body:          bytes.NewReader(body),
		ContentLength: int64(len(body)),
		Headers:       GetMetricsCsv200ResponseHeaders{CacheControl: &noStore, ContentDisposition: &disposition},
	}, nil
}

func cell(c model.Cell) MetricCell {
	if c.Suppressed {
		return MetricCell{Suppressed: true}
	}
	members, value := c.Members, float32(c.Value)
	return MetricCell{Members: &members, Value: &value}
}

func score(s model.Score) ScoreAggregate {
	return ScoreAggregate{Mean: cell(s.Mean), Median: cell(s.Median)}
}

func buckets(in []model.Bucket) []BucketCell {
	out := make([]BucketCell, 0, len(in))
	for _, b := range in {
		out = append(out, BucketCell{Bucket: openapi_types.Date{Time: b.Start}, Cell: cell(b.Cell)})
	}
	return out
}

func toAPI(m model.Metrics) Metrics {
	var out Metrics
	out.From = openapi_types.Date{Time: m.Period.From}
	out.To = openapi_types.Date{Time: m.Period.To}
	out.K = m.K
	out.Stress.Baseline = score(m.StressBaseline)
	out.Stress.Week8 = score(m.StressWeek8)
	out.Isolation.Before = score(m.IsolationBefore)
	out.Isolation.After = score(m.IsolationAfter)
	out.Activity.Weekly = buckets(m.ActiveWeekly)
	out.Activity.Monthly = buckets(m.ActiveMonthly)
	out.Activity.Retention4w = cell(m.Retention4w)
	out.Activity.Retention8w = cell(m.Retention8w)
	out.Tea.Requests = cell(m.TeaRequests)
	out.Tea.Pairs = cell(m.TeaPairs)
	out.Tea.Meetings = cell(m.TeaMeetings)
	out.Sos.Raised = cell(m.SosRaised)
	out.Sos.ClosedShare = cell(m.SosClosedShare)
	out.Sos.FirstResponseMedianMinutes = cell(m.SosFirstResponseMedianMinutes)
	out.Escalations.Opened = cell(m.EscalationsOpened)
	out.Escalations.TakenWithin15mShare = cell(m.EscalationsTakenWithin15mShare)
	out.Rights.AnsweredFromKnowledge = cell(m.RightsAnsweredFromKnowledge)
	out.Rights.HelpedShare = cell(m.RightsHelpedShare)
	return out
}
