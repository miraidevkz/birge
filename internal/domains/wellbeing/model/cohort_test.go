package model_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/wellbeing/model"
)

func TestStatsOf(t *testing.T) {
	tests := []struct {
		name   string
		values []int
		want   model.ScoreStats
	}{
		{"empty", nil, model.ScoreStats{}},
		{"one", []int{7}, model.ScoreStats{Count: 1, Mean: 7, Median: 7}},
		{"odd unsorted", []int{9, 1, 5}, model.ScoreStats{Count: 3, Mean: 5, Median: 5}},
		{"even", []int{4, 1, 3, 10}, model.ScoreStats{Count: 4, Mean: 4.5, Median: 3.5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.StatsOf(tt.values); got != tt.want {
				t.Fatalf("StatsOf(%v) = %+v, want %+v", tt.values, got, tt.want)
			}
		})
	}
}

func TestStatsOfDoesNotReorderInput(t *testing.T) {
	in := []int{3, 1, 2}
	model.StatsOf(in)
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Fatalf("input reordered: %v", in)
	}
}

func TestBuildCohortAggregates(t *testing.T) {
	base := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	a, b, c := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := func(m uuid.UUID, score int, at time.Duration) model.CohortCheckin {
		return model.CohortCheckin{MemberID: m, PSS4: score, CompletedAt: base.Add(at)}
	}
	tests := []struct {
		name         string
		rows         []model.CohortCheckin
		wantBaseline model.ScoreStats
		wantWeek8    model.ScoreStats
	}{
		{
			name: "empty",
		},
		{
			name:         "only baseline",
			rows:         []model.CohortCheckin{row(a, 10, 0), row(a, 9, week)},
			wantBaseline: model.ScoreStats{Count: 1, Mean: 10, Median: 10},
		},
		{
			name: "nearest to 8 weeks wins",
			rows: []model.CohortCheckin{
				row(a, 12, 0), row(a, 11, 7*week), row(a, 6, 8*week+time.Hour), row(a, 2, 9*week),
			},
			wantBaseline: model.ScoreStats{Count: 1, Mean: 12, Median: 12},
			wantWeek8:    model.ScoreStats{Count: 1, Mean: 6, Median: 6},
		},
		{
			name:         "tie keeps the earlier check-in",
			rows:         []model.CohortCheckin{row(a, 12, 0), row(a, 5, 7*week), row(a, 3, 9*week)},
			wantBaseline: model.ScoreStats{Count: 1, Mean: 12, Median: 12},
			wantWeek8:    model.ScoreStats{Count: 1, Mean: 5, Median: 5},
		},
		{
			name: "outside the window is not a follow-up",
			rows: []model.CohortCheckin{
				row(a, 12, 0), row(a, 1, 6*week-time.Minute), row(a, 1, 10*week+time.Minute),
			},
			wantBaseline: model.ScoreStats{Count: 1, Mean: 12, Median: 12},
		},
		{
			name: "window edges count",
			rows: []model.CohortCheckin{
				row(a, 10, 0), row(a, 4, 6*week),
				row(b, 8, time.Hour), row(b, 2, time.Hour+10*week),
				row(c, 6, 2*time.Hour),
			},
			wantBaseline: model.ScoreStats{Count: 3, Mean: 8, Median: 8},
			wantWeek8:    model.ScoreStats{Count: 2, Mean: 3, Median: 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.BuildCohortAggregates(tt.rows)
			if got.StressBaseline != tt.wantBaseline || got.StressWeek8 != tt.wantWeek8 {
				t.Fatalf("got baseline %+v week8 %+v, want %+v / %+v",
					got.StressBaseline, got.StressWeek8, tt.wantBaseline, tt.wantWeek8)
			}
			if got.IsolationBefore != (model.ScoreStats{}) || got.IsolationAfter != (model.ScoreStats{}) {
				t.Fatalf("isolation must stay empty without an isolation item: %+v", got)
			}
		})
	}
}
