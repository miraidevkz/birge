package model

import (
	"slices"
	"time"

	"github.com/google/uuid"
)

// Cohort follow-up window (ADR-0024, requirements § 9 "PSS-4 at start and after 8 weeks").
// The follow-up is the member's check-in nearest to CohortFollowUpTarget after her first
// one, taken only from [CohortFollowUpMin, CohortFollowUpMax]: without a lower bound a
// member with two check-ins a week apart would count her second week as "after 8 weeks".
const (
	CohortFollowUpTarget = 8 * 7 * 24 * time.Hour
	CohortFollowUpMin    = 6 * 7 * 24 * time.Hour
	CohortFollowUpMax    = 10 * 7 * 24 * time.Hour
)

// ScoreStats is a cohort aggregate of one score: how many members it is built from,
// their mean and their median. It never carries a per-member value; the caller
// (analytics) suppresses it when Count is below its threshold.
type ScoreStats struct {
	Count  int
	Mean   float64
	Median float64
}

// CohortAggregates are the health aggregates wellbeing shares with analytics (ADR-0024).
//
// The cohort is every member whose first check-in falls in the requested period.
// Isolation stays empty (Count 0) while the questionnaire has no isolation item:
// the current PHQ-4 + PSS-4 set carries none, and individual answers are sealed,
// so there is nothing to aggregate until a plain isolation score is stored.
type CohortAggregates struct {
	StressBaseline  ScoreStats
	StressWeek8     ScoreStats
	IsolationBefore ScoreStats
	IsolationAfter  ScoreStats
}

// CohortCheckin is one check-in's score row used only inside wellbeing to build
// CohortAggregates; it never leaves the service package.
type CohortCheckin struct {
	MemberID    uuid.UUID
	PSS4        int
	CompletedAt time.Time
}

// BuildCohortAggregates reduces rows to cohort aggregates. rows must be ordered by
// member, then completed_at ascending, and hold only members whose first check-in is
// in the period (the store query guarantees both). For each member the first row is
// her baseline; her follow-up is the row nearest to baseline+8 weeks inside the
// window, the earlier one on a tie.
func BuildCohortAggregates(rows []CohortCheckin) CohortAggregates {
	var baseline, week8 []int
	for start := 0; start < len(rows); {
		end := start + 1
		for end < len(rows) && rows[end].MemberID == rows[start].MemberID {
			end++
		}
		first := rows[start]
		baseline = append(baseline, first.PSS4)
		if v, ok := followUp(first.CompletedAt, rows[start+1:end]); ok {
			week8 = append(week8, v)
		}
		start = end
	}
	return CohortAggregates{
		StressBaseline: StatsOf(baseline),
		StressWeek8:    StatsOf(week8),
	}
}

func followUp(baselineAt time.Time, later []CohortCheckin) (int, bool) {
	target := baselineAt.Add(CohortFollowUpTarget)
	best, found := 0, false
	var bestDist time.Duration
	for _, c := range later {
		since := c.CompletedAt.Sub(baselineAt)
		if since < CohortFollowUpMin || since > CohortFollowUpMax {
			continue
		}
		dist := c.CompletedAt.Sub(target).Abs()
		// Strict less keeps the earlier check-in on a tie (rows are ascending).
		if !found || dist < bestDist {
			best, bestDist, found = c.PSS4, dist, true
		}
	}
	return best, found
}

// StatsOf returns count, mean and median of values (zero stats for none).
func StatsOf(values []int) ScoreStats {
	n := len(values)
	if n == 0 {
		return ScoreStats{}
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	sum := 0
	for _, v := range sorted {
		sum += v
	}
	var median float64
	if n%2 == 1 {
		median = float64(sorted[n/2])
	} else {
		median = float64(sorted[n/2-1]+sorted[n/2]) / 2
	}
	return ScoreStats{Count: n, Mean: float64(sum) / float64(n), Median: median}
}
