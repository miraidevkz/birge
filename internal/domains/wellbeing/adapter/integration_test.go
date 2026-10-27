package adapter_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/wellbeing/adapter"
	"birge/internal/domains/wellbeing/model"
	"birge/internal/domains/wellbeing/service"
	"birge/internal/domains/wellbeing/store"
	"birge/internal/domains/wellbeing/wellbeingtest"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func testSettings() service.Settings {
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
	return service.Settings{
		Questionnaire: model.Questionnaire{
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
		},
		ReverseScoreQuestionIDs: []string{"pss_2", "pss_3"},
		CrisisItemQuestionID:    "phq_4",
		Thresholds: map[string]model.InstrumentThresholds{
			model.InstrumentPHQ4: {ElevatedAt: 3, CrisisAt: 9},
			model.InstrumentPSS4: {ElevatedAt: 8, CrisisAt: 13},
		},
		FeedbackByFlag: map[string]model.Feedback{
			model.FlagNone:     {FeedbackKey: "wellbeing.feedback.none", Suggestion: model.SuggestionPractice},
			model.FlagElevated: {FeedbackKey: "wellbeing.feedback.elevated", Suggestion: model.SuggestionPsychologist},
			model.FlagCrisis:   {FeedbackKey: "wellbeing.feedback.crisis", Suggestion: model.SuggestionNone},
		},
		TimeZone:         "Asia/Almaty",
		PracticesBaseURL: "https://objects.example.invalid/practices",
		CrisisPhones:     []model.CrisisPhone{{Label: "Emergency", Number: "+77000000000"}},
	}
}

func answers(values ...int) []model.Answer {
	ids := []string{"phq_1", "phq_2", "phq_3", "phq_4", "pss_1", "pss_2", "pss_3", "pss_4"}
	out := make([]model.Answer, len(ids))
	for i, id := range ids {
		out[i] = model.Answer{QuestionID: id, Value: values[i]}
	}
	return out
}

func TestCheckinAndSchedulePostgres(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	pg := &adapter.PostgresStore{Pool: pool}
	sealer := &wellbeingtest.FakeSealer{}
	crisis := &wellbeingtest.FakeCrisisOpener{NextID: uuid.Must(uuid.NewV7())}
	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Fatal(err)
	}
	nowLocal := time.Date(2026, 10, 28, 8, 0, 0, 0, loc)
	clk := &fixedClock{t: nowLocal.UTC()}
	svc := &service.Service{
		Store:    pg,
		Sealer:   sealer,
		Crisis:   crisis,
		Clock:    clk,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: testSettings(),
	}
	member := uuid.Must(uuid.NewV7())

	sched, err := svc.SetSchedule(ctx, member, 3, 9, true)
	if err != nil {
		t.Fatal(err)
	}
	if !sched.Enabled {
		t.Fatal("expected enabled schedule")
	}
	var timerCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM platform.timers WHERE kind = $1 AND subject_id = $2 AND done_at IS NULL`,
		model.TimerReminder, member,
	).Scan(&timerCount); err != nil {
		t.Fatal(err)
	}
	if timerCount != 1 {
		t.Fatalf("timers=%d", timerCount)
	}

	result, err := svc.SubmitCheckin(ctx, member, uuid.Must(uuid.NewV7()), "v1", answers(1, 1, 1, 0, 1, 0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if result.Flag != model.FlagElevated {
		t.Fatalf("flag=%s", result.Flag)
	}

	var events int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM platform.outbox WHERE event_type = $1`,
		model.EventCheckinCompleted,
	).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("outbox events=%d", events)
	}

	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM platform.outbox WHERE event_type = $1`,
		model.EventCheckinCompleted,
	).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var data model.CheckinCompletedData
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Flag != model.FlagElevated || data.CheckinID != result.ID || data.MemberID != member {
		t.Fatalf("payload=%+v raw=%s", data, payload)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 3 {
		t.Fatalf("event must carry only member_id, checkin_id, flag; got %v", raw)
	}

	points, err := svc.GetHistory(ctx, member, 8)
	if err != nil || len(points) != 1 {
		t.Fatalf("history=%v err=%v", points, err)
	}

	q := store.New(pool)
	if _, err := q.InsertPractice(ctx, store.InsertPracticeParams{
		Lang: model.LangKK, TitleKey: "practice.ground", DurationSeconds: 90, ObjectKey: "kk/ground.ogg",
	}); err != nil {
		t.Fatal(err)
	}
	practices, err := svc.ListPractices(ctx, member, model.LangKK)
	if err != nil || len(practices) != 1 {
		t.Fatalf("practices=%v err=%v", practices, err)
	}
	if practices[0].URL != "https://objects.example.invalid/practices/kk/ground.ogg" {
		t.Fatalf("url=%s", practices[0].URL)
	}
}

func TestReminderTimerHandlerPostgres(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	pg := &adapter.PostgresStore{Pool: pool}
	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Fatal(err)
	}
	due := time.Date(2026, 10, 28, 9, 0, 0, 0, loc)
	clk := &fixedClock{t: due.Add(-time.Hour).UTC()}
	svc := &service.Service{
		Store:    pg,
		Sealer:   &wellbeingtest.FakeSealer{},
		Crisis:   &wellbeingtest.FakeCrisisOpener{},
		Clock:    clk,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: testSettings(),
	}
	handlers := &adapter.TimerHandlers{Service: svc}
	member := uuid.Must(uuid.NewV7())

	if _, err := svc.SetSchedule(ctx, member, 3, 9, true); err != nil {
		t.Fatal(err)
	}

	clk.t = due.UTC()
	worker := &timers.Worker{
		Pool:     pool,
		Clock:    clk,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: handlers.Handlers(),
	}
	fired, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("expected timer to fire")
	}

	var deliveries int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM platform.outbox WHERE event_type = $1`,
		model.EventDeliveryRequested,
	).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 {
		t.Fatalf("delivery events=%d", deliveries)
	}

	var active int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM platform.timers WHERE kind = $1 AND subject_id = $2 AND done_at IS NULL`,
		model.TimerReminder, member,
	).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("next timer active=%d", active)
	}
}
