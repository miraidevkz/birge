package service

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
	"birge/internal/platform/staffauth"
)

// Audit codes of report reads (package platform/audit).
const (
	AuditActionRead   = "analytics.metrics_read"
	AuditActionExport = "analytics.metrics_export"
	AuditObject       = "analytics.metrics"
)

// FactsConsumer is the platform.processed_events consumer name of HandleEvent.
const FactsConsumer = "analytics.Facts"

// Settings is the analytics configuration.
type Settings struct {
	// K is the small-cell threshold; 0 means model.DefaultK.
	K int
	// TimeZone is the deployment IANA zone of periods, weeks and months
	// (default model.DefaultTimeZone).
	TimeZone string
	// Key is the analytics pseudonym key (BIRGE_ANALYTICS_KEY_FILE). Only
	// the worker has it; HandleEvent refuses to run without it.
	Key []byte
	// EventsMaxAge is how long the EVENTS stream keeps an event (wiring
	// Config.EventsMaxAge, BIRGE_EVENTS_MAX_AGE, the MaxAge the worker gives
	// the stream). A deleted member's pseudonym stays in erased_actors this
	// long: an event of hers can reach analytics only while the stream still
	// holds it. Zero keeps erased pseudonyms (never purged): without the
	// stream's limit there is no safe point to forget them.
	EventsMaxAge time.Duration
}

// Service implements the analytics use cases. Store and Clock are required;
// Cohorts may be nil (stress and isolation are then suppressed).
type Service struct {
	Store    port.Store
	Cohorts  port.Cohorts
	Clock    clock.Clock
	Logger   *slog.Logger
	Settings Settings
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

// K returns the small-cell threshold in effect.
func (s *Service) K() int {
	if s.Settings.K > 0 {
		return s.Settings.K
	}
	return model.DefaultK
}

func (s *Service) location() (*time.Location, error) {
	name := s.Settings.TimeZone
	if name == "" {
		name = model.DefaultTimeZone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("analytics: load time zone %q: %w", name, err)
	}
	return loc, nil
}

// requireAdmin authorizes a report: staff with the admin role and MFA.
// Everything else is refused (deny by default): no principal or a member
// session is unauthorized, other staff roles or no MFA are forbidden.
func requireAdmin(p auth.Principal) error {
	if !p.Staff || p.IdentityID == uuid.Nil {
		return model.ErrUnauthorized
	}
	if !p.MFA || !slices.Contains(p.Roles, staffauth.RoleAdmin) {
		return model.ErrForbidden
	}
	return nil
}

// Metrics returns the suppressed report for [from, to] (dates, inclusive).
// The read is audited (AuditActionRead) in the transaction of the queries.
func (s *Service) Metrics(ctx context.Context, p auth.Principal, from, to time.Time) (model.Metrics, error) {
	return s.report(ctx, p, from, to, AuditActionRead)
}

// MetricsCSV returns the suppressed report as CSV and its attachment file
// name. The export is audited (AuditActionExport).
func (s *Service) MetricsCSV(ctx context.Context, p auth.Principal, from, to time.Time) ([]byte, string, error) {
	m, err := s.report(ctx, p, from, to, AuditActionExport)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	if err := model.WriteCSV(&buf, m); err != nil {
		return nil, "", fmt.Errorf("analytics: write csv: %w", err)
	}
	return buf.Bytes(), model.CSVFilename(m.Period), nil
}

func (s *Service) report(ctx context.Context, p auth.Principal, from, to time.Time, action string) (model.Metrics, error) {
	if err := requireAdmin(p); err != nil {
		return model.Metrics{}, err
	}
	period, err := model.NewPeriod(from, to)
	if err != nil {
		return model.Metrics{}, err
	}
	loc, err := s.location()
	if err != nil {
		return model.Metrics{}, err
	}
	start, end := period.Bounds(loc)

	// Wellbeing is asked first and outside the transaction: no transaction
	// is held across another context.
	var cohorts model.CohortAggregates
	if s.Cohorts != nil {
		if cohorts, err = s.Cohorts.CohortAggregates(ctx, start, end); err != nil {
			return model.Metrics{}, fmt.Errorf("analytics: cohort aggregates: %w", err)
		}
	}

	m := model.Metrics{
		Period:          period,
		StressBaseline:  model.ScoreOf(cohorts.StressBaseline),
		StressWeek8:     model.ScoreOf(cohorts.StressWeek8),
		IsolationBefore: model.ScoreOf(cohorts.IsolationBefore),
		IsolationAfter:  model.ScoreOf(cohorts.IsolationAfter),
	}
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		if err := tx.Audit(ctx, port.AuditEntry{
			ActorID: p.IdentityID, ActorRole: staffauth.RoleAdmin, Action: action, ObjectType: AuditObject,
		}); err != nil {
			return err
		}
		return s.fill(ctx, tx, &m, loc, start, end)
	})
	if err != nil {
		return model.Metrics{}, err
	}
	out := model.Suppress(m, s.K())
	s.log().InfoContext(ctx, "analytics report", "event", "AnalyticsReport", "action", action, "staff_id", p.IdentityID)
	return out, nil
}

// fill computes every event-based cell of m inside tx.
func (s *Service) fill(ctx context.Context, tx port.Tx, m *model.Metrics, loc *time.Location, start, end time.Time) error {
	weekly, err := tx.ActiveMembers(ctx, "week", loc.String(), start, end)
	if err != nil {
		return err
	}
	for _, w := range m.Period.Weeks() {
		n := weekly[w]
		m.ActiveWeekly = append(m.ActiveWeekly, model.Bucket{Start: w, Cell: model.Count(n, n)})
	}
	monthly, err := tx.ActiveMembers(ctx, "month", loc.String(), start, end)
	if err != nil {
		return err
	}
	for _, mo := range m.Period.Months() {
		n := monthly[mo]
		m.ActiveMonthly = append(m.ActiveMonthly, model.Bucket{Start: mo, Cell: model.Count(n, n)})
	}

	ret, err := tx.Retention(ctx, start, end, s.now())
	if err != nil {
		return err
	}
	m.Retention4w = model.Share(ret.Eligible4w, ret.Retained4w, ret.Eligible4w)
	m.Retention8w = model.Share(ret.Eligible8w, ret.Retained8w, ret.Eligible8w)

	counts, err := tx.CountFacts(ctx, start, end, []string{
		model.EventTeaRequested, model.EventMatchAccepted, model.EventAcquaintanceEstablished,
	})
	if err != nil {
		return err
	}
	// Requests, pairs and meetings are counted by their subject (one per
	// request, match or acquaintance), members by distinct actors.
	tea := func(t string) model.Cell { c := counts[t]; return model.Count(c.Members, c.Subjects) }
	m.TeaRequests = tea(model.EventTeaRequested)
	m.TeaPairs = tea(model.EventMatchAccepted)
	m.TeaMeetings = tea(model.EventAcquaintanceEstablished)

	sos, err := tx.SosOutcomes(ctx, start, end)
	if err != nil {
		return err
	}
	m.SosRaised = model.Count(sos.RaisedMembers, sos.Raised)
	m.SosClosedShare = model.Share(sos.RaisedMembers, sos.Closed, sos.Raised)
	m.SosFirstResponseMedianMinutes = model.Cell{Members: sos.RespondedMembers, Value: sos.MedianMinutesToFirstResponse}

	esc, err := tx.EscalationOutcomes(ctx, start, end, model.EscalationTakenWithin)
	if err != nil {
		return err
	}
	m.EscalationsOpened = model.Count(esc.Members, esc.Opened)
	m.EscalationsTakenWithin15mShare = model.Share(esc.Members, esc.TakenWithin, esc.Opened)

	// Rights (requirements section 9): answers closed by the knowledge base,
	// and the share of rated rights answers rated helped. Members are the
	// askers (QuestionAnswered actor); a rating counts for the member of the
	// answer it rates.
	rights, err := tx.RightsOutcomes(ctx, start, end)
	if err != nil {
		return err
	}
	m.RightsAnsweredFromKnowledge = model.Count(rights.KnowledgeMembers, rights.AnsweredFromKnowledge)
	m.RightsHelpedShare = model.Share(rights.RatedMembers, rights.Helped, rights.Rated)
	return nil
}
