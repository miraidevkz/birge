package model

import (
	"fmt"
	"time"
)

// Period is an inclusive range of civil days in the deployment time zone,
// as the contract's from and to parameters are.
type Period struct {
	From time.Time // a date (midnight, location ignored)
	To   time.Time // a date, inclusive
}

// NewPeriod validates from and to: to is not before from and at most
// MaxPeriodMonths after it (requirements section 10, contract MetricsTo).
func NewPeriod(from, to time.Time) (Period, error) {
	f, t := civil(from), civil(to)
	if f.IsZero() || t.IsZero() || t.Before(f) {
		return Period{}, fmt.Errorf("%w: to is before from", ErrInvalidInput)
	}
	if t.After(f.AddDate(0, MaxPeriodMonths, 0)) {
		return Period{}, fmt.Errorf("%w: period longer than %d months", ErrInvalidInput, MaxPeriodMonths)
	}
	return Period{From: f, To: t}, nil
}

func civil(d time.Time) time.Time {
	if d.IsZero() {
		return time.Time{}
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// Bounds returns the half-open instant range [start, end) of the period in loc.
func (p Period) Bounds(loc *time.Location) (start, end time.Time) {
	start = time.Date(p.From.Year(), p.From.Month(), p.From.Day(), 0, 0, 0, 0, loc)
	end = time.Date(p.To.Year(), p.To.Month(), p.To.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	return start, end
}

// Weeks returns the Monday of every week that overlaps the period.
func (p Period) Weeks() []time.Time {
	first := p.From.AddDate(0, 0, -((int(p.From.Weekday()) + 6) % 7))
	var out []time.Time
	for d := first; !d.After(p.To); d = d.AddDate(0, 0, 7) {
		out = append(out, d)
	}
	return out
}

// Months returns the first day of every month that overlaps the period.
func (p Period) Months() []time.Time {
	var out []time.Time
	for d := time.Date(p.From.Year(), p.From.Month(), 1, 0, 0, 0, 0, time.UTC); !d.After(p.To); d = d.AddDate(0, 1, 0) {
		out = append(out, d)
	}
	return out
}

// Cell is one aggregate: the number of distinct members it is built from
// and its value. After Suppress, a cell below k has Suppressed set and
// carries neither number.
type Cell struct {
	Members    int
	Value      float64
	Suppressed bool
}

// Count builds a cell whose value is a count.
func Count(members, n int) Cell { return Cell{Members: members, Value: float64(n)} }

// Share builds a cell whose value is part/whole (0 when whole is 0).
func Share(members, part, whole int) Cell {
	if whole == 0 {
		return Cell{Members: members}
	}
	return Cell{Members: members, Value: float64(part) / float64(whole)}
}

// Score is the mean and median of one score over a cohort.
type Score struct {
	Mean   Cell
	Median Cell
}

// Bucket is one week or month of a series.
type Bucket struct {
	Start time.Time // Monday or first day of the month (a date)
	Cell  Cell
}

// Metrics is the report of requirements section 9 (contract Metrics).
type Metrics struct {
	Period Period
	K      int

	StressBaseline, StressWeek8     Score
	IsolationBefore, IsolationAfter Score

	ActiveWeekly, ActiveMonthly []Bucket
	Retention4w, Retention8w    Cell

	TeaRequests, TeaPairs, TeaMeetings Cell

	SosRaised, SosClosedShare, SosFirstResponseMedianMinutes Cell

	EscalationsOpened, EscalationsTakenWithin15mShare Cell

	RightsAnsweredFromKnowledge, RightsHelpedShare Cell
}

// ScoreStats is one cohort aggregate as wellbeing reports it.
type ScoreStats struct {
	Count  int
	Mean   float64
	Median float64
}

// CohortAggregates are the stress and isolation aggregates from wellbeing
// (ADR-0024): count, mean and median only, never per-member values.
type CohortAggregates struct {
	StressBaseline, StressWeek8     ScoreStats
	IsolationBefore, IsolationAfter ScoreStats
}

// ScoreOf turns a cohort aggregate into a Score built from Count members.
func ScoreOf(s ScoreStats) Score {
	return Score{
		Mean:   Cell{Members: s.Count, Value: s.Mean},
		Median: Cell{Members: s.Count, Value: s.Median},
	}
}

// Suppress applies small-cell suppression to every cell of m and records k.
// It is the only place the rule is applied: a cell built from fewer than k
// members keeps only Suppressed=true (ADR-0024). k below 1 is treated as 1,
// so a cell built from nobody is always suppressed.
func Suppress(m Metrics, k int) Metrics {
	if k < 1 {
		k = 1
	}
	m.K = k
	m.ActiveWeekly = append([]Bucket(nil), m.ActiveWeekly...)
	m.ActiveMonthly = append([]Bucket(nil), m.ActiveMonthly...)
	for _, c := range m.cells() {
		if c.Members < k {
			*c = Cell{Suppressed: true}
		}
	}
	return m
}

// cells returns a pointer to every cell of m, in report order.
func (m *Metrics) cells() []*Cell {
	out := []*Cell{
		&m.StressBaseline.Mean, &m.StressBaseline.Median, &m.StressWeek8.Mean, &m.StressWeek8.Median,
		&m.IsolationBefore.Mean, &m.IsolationBefore.Median, &m.IsolationAfter.Mean, &m.IsolationAfter.Median,
	}
	for i := range m.ActiveWeekly {
		out = append(out, &m.ActiveWeekly[i].Cell)
	}
	for i := range m.ActiveMonthly {
		out = append(out, &m.ActiveMonthly[i].Cell)
	}
	return append(out,
		&m.Retention4w, &m.Retention8w,
		&m.TeaRequests, &m.TeaPairs, &m.TeaMeetings,
		&m.SosRaised, &m.SosClosedShare, &m.SosFirstResponseMedianMinutes,
		&m.EscalationsOpened, &m.EscalationsTakenWithin15mShare,
		&m.RightsAnsweredFromKnowledge, &m.RightsHelpedShare,
	)
}
