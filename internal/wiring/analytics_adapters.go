package wiring

import (
	"context"
	"time"

	analyticsmodel "birge/internal/domains/analytics/model"
	analyticsport "birge/internal/domains/analytics/port"
	wellbeingmodel "birge/internal/domains/wellbeing/model"
	wellbeingservice "birge/internal/domains/wellbeing/service"
)

// WellbeingCohorts implements analytics port.Cohorts with the wellbeing
// service (ADR-0024): analytics receives counts, means and medians only and
// never reads wellbeing tables.
type WellbeingCohorts struct {
	Wellbeing *wellbeingservice.Service
}

var _ analyticsport.Cohorts = (*WellbeingCohorts)(nil)

// CohortAggregates implements analytics port.Cohorts.
func (c *WellbeingCohorts) CohortAggregates(ctx context.Context, from, to time.Time) (analyticsmodel.CohortAggregates, error) {
	a, err := c.Wellbeing.CohortAggregates(ctx, from, to)
	if err != nil {
		return analyticsmodel.CohortAggregates{}, err
	}
	conv := func(s wellbeingmodel.ScoreStats) analyticsmodel.ScoreStats {
		return analyticsmodel.ScoreStats{Count: s.Count, Mean: s.Mean, Median: s.Median}
	}
	return analyticsmodel.CohortAggregates{
		StressBaseline:  conv(a.StressBaseline),
		StressWeek8:     conv(a.StressWeek8),
		IsolationBefore: conv(a.IsolationBefore),
		IsolationAfter:  conv(a.IsolationAfter),
	}, nil
}
