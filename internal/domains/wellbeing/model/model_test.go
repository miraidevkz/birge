package model_test

import (
	"testing"
	"time"

	"birge/internal/domains/wellbeing/model"
)

func sampleQuestionnaire() model.Questionnaire {
	opts := []model.QuestionOption{
		{Value: 0, TextKey: "opt.0"},
		{Value: 1, TextKey: "opt.1"},
		{Value: 2, TextKey: "opt.2"},
		{Value: 3, TextKey: "opt.3"},
	}
	pssOpts := []model.QuestionOption{
		{Value: 0, TextKey: "pss.0"},
		{Value: 1, TextKey: "pss.1"},
		{Value: 2, TextKey: "pss.2"},
		{Value: 3, TextKey: "pss.3"},
		{Value: 4, TextKey: "pss.4"},
	}
	return model.Questionnaire{
		Version: "v1",
		Questions: []model.Question{
			{ID: "phq_1", Instrument: model.InstrumentPHQ4, TextKey: "phq.1", Options: opts},
			{ID: "phq_2", Instrument: model.InstrumentPHQ4, TextKey: "phq.2", Options: opts},
			{ID: "phq_3", Instrument: model.InstrumentPHQ4, TextKey: "phq.3", Options: opts},
			{ID: "phq_4", Instrument: model.InstrumentPHQ4, TextKey: "phq.4", Options: opts},
			{ID: "pss_1", Instrument: model.InstrumentPSS4, TextKey: "pss.1", Options: pssOpts},
			{ID: "pss_2", Instrument: model.InstrumentPSS4, TextKey: "pss.2", Options: pssOpts},
			{ID: "pss_3", Instrument: model.InstrumentPSS4, TextKey: "pss.3", Options: pssOpts},
			{ID: "pss_4", Instrument: model.InstrumentPSS4, TextKey: "pss.4", Options: pssOpts},
		},
	}
}

func allAnswers(values ...int) []model.Answer {
	ids := []string{"phq_1", "phq_2", "phq_3", "phq_4", "pss_1", "pss_2", "pss_3", "pss_4"}
	out := make([]model.Answer, len(ids))
	for i, id := range ids {
		out[i] = model.Answer{QuestionID: id, Value: values[i]}
	}
	return out
}

func TestValidateAnswersRequiresExactSet(t *testing.T) {
	t.Parallel()
	q := sampleQuestionnaire()
	if err := model.ValidateAnswers(q, "v1", allAnswers(0, 0, 0, 0, 0, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateAnswers(q, "other", allAnswers(0, 0, 0, 0, 0, 0, 0, 0)); err == nil {
		t.Fatal("expected version error")
	}
	dup := allAnswers(0, 0, 0, 0, 0, 0, 0, 0)
	dup[7].QuestionID = "phq_1"
	if err := model.ValidateAnswers(q, "v1", dup); err == nil {
		t.Fatal("expected duplicate error")
	}
	short := allAnswers(0, 0, 0, 0, 0, 0, 0, 0)[:7]
	if err := model.ValidateAnswers(q, "v1", short); err == nil {
		t.Fatal("expected count error")
	}
	bad := allAnswers(0, 0, 0, 0, 0, 0, 0, 0)
	bad[0].Value = 9
	if err := model.ValidateAnswers(q, "v1", bad); err == nil {
		t.Fatal("expected option error")
	}
}

func TestScoreReverseItemsAndThresholds(t *testing.T) {
	t.Parallel()
	q := sampleQuestionnaire()
	thresholds := map[string]model.InstrumentThresholds{
		model.InstrumentPHQ4: {ElevatedAt: 3, CrisisAt: 9},
		model.InstrumentPSS4: {ElevatedAt: 8, CrisisAt: 13},
	}
	// PSS reverse on pss_2 and pss_3: raw 0 → contrib 4.
	answers := allAnswers(1, 1, 1, 0, 1, 0, 0, 1) // phq=3, pss=1+4+4+1=10
	out, err := model.Score(q, answers, []string{"pss_2", "pss_3"}, "", thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if out.PHQ4 != 3 || out.PSS4 != 10 {
		t.Fatalf("scores phq=%d pss=%d", out.PHQ4, out.PSS4)
	}
	if out.Flag != model.FlagElevated {
		t.Fatalf("flag=%s want elevated", out.Flag)
	}

	crisisByScore := allAnswers(3, 3, 3, 3, 4, 4, 4, 4) // phq=12 crisis
	out, err = model.Score(q, crisisByScore, nil, "", thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if out.Flag != model.FlagCrisis {
		t.Fatalf("flag=%s want crisis", out.Flag)
	}
}

func TestScoreCrisisItemMaxForcesCrisis(t *testing.T) {
	t.Parallel()
	q := sampleQuestionnaire()
	thresholds := map[string]model.InstrumentThresholds{
		model.InstrumentPHQ4: {ElevatedAt: 6, CrisisAt: 12},
		model.InstrumentPSS4: {ElevatedAt: 10, CrisisAt: 16},
	}
	// Low scores but crisis item phq_4 at max (3).
	answers := allAnswers(0, 0, 0, 3, 0, 0, 0, 0)
	out, err := model.Score(q, answers, nil, "phq_4", thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if out.Flag != model.FlagCrisis {
		t.Fatalf("flag=%s want crisis", out.Flag)
	}
	if out.PHQ4 != 3 {
		t.Fatalf("phq=%d", out.PHQ4)
	}
}

func TestNextOccurrenceAcrossWeek(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Fatal(err)
	}
	// Wednesday 2026-10-28 10:00 Almaty; ask for Monday 09:00 → next Monday 2026-11-02 09:00.
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, loc)
	next := model.NextOccurrence(now, loc, 1, 9) // ISO Monday
	want := time.Date(2026, 11, 2, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next=%v want %v", next, want)
	}

	// Same weekday later hour today still ahead.
	now = time.Date(2026, 10, 28, 8, 0, 0, 0, loc) // Wednesday
	next = model.NextOccurrence(now, loc, 3, 9)    // ISO Wednesday
	want = time.Date(2026, 10, 28, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("same-day next=%v want %v", next, want)
	}

	// Same weekday hour already passed → next week.
	now = time.Date(2026, 10, 28, 10, 0, 0, 0, loc)
	next = model.NextOccurrence(now, loc, 3, 9)
	want = time.Date(2026, 11, 4, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next-week=%v want %v", next, want)
	}
}

func TestNextWeeklyOccurrence(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Fatal(err)
	}
	due := time.Date(2026, 10, 28, 9, 0, 0, 0, loc) // Wednesday
	next := model.NextWeeklyOccurrence(due, loc, 3, 9)
	want := time.Date(2026, 11, 4, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("weekly=%v want %v", next, want)
	}
}
