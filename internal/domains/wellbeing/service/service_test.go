package service_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/wellbeing/model"
	"birge/internal/domains/wellbeing/service"
	"birge/internal/domains/wellbeing/wellbeingtest"
	"birge/internal/platform/crypto"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func sampleSettings() service.Settings {
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
		CrisisPhones: []model.CrisisPhone{
			{Label: "Emergency", Number: "+77000000000"},
		},
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

func newService(t *testing.T, clk *fixedClock, logBuf *bytes.Buffer) (*service.Service, *wellbeingtest.MemoryStore, *wellbeingtest.FakeSealer, *wellbeingtest.FakeCrisisOpener) {
	t.Helper()
	store := wellbeingtest.NewMemoryStore()
	sealer := &wellbeingtest.FakeSealer{}
	crisis := &wellbeingtest.FakeCrisisOpener{NextID: uuid.Must(uuid.NewV7())}
	var handler slog.Handler = slog.NewTextHandler(io.Discard, nil)
	if logBuf != nil {
		handler = slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	svc := &service.Service{
		Store:    store,
		Sealer:   sealer,
		Crisis:   crisis,
		Clock:    clk,
		Logger:   slog.New(handler),
		Settings: sampleSettings(),
	}
	return svc, store, sealer, crisis
}

func TestSubmitCheckinScoresAndEmitsFlagOnly(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc, store, sealer, crisis := newService(t, clk, nil)
	member := uuid.Must(uuid.NewV7())
	key := uuid.Must(uuid.NewV7())
	ctx := context.Background()

	result, err := svc.SubmitCheckin(ctx, member, key, "v1", answers(1, 1, 1, 0, 1, 0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if result.Flag != model.FlagElevated {
		t.Fatalf("flag=%s", result.Flag)
	}
	if result.FeedbackKey == "" || result.Suggestion != model.SuggestionPsychologist {
		t.Fatalf("result=%+v", result)
	}
	if crisis.Len() != 0 {
		t.Fatal("elevated must not open escalation")
	}
	if store.EventCount(model.EventCheckinCompleted) != 1 {
		t.Fatal("expected CheckinCompleted")
	}
	ev := store.Events()[0].Data.(model.CheckinCompletedData)
	if ev.Flag != model.FlagElevated || ev.CheckinID != result.ID {
		t.Fatalf("event=%+v", ev)
	}
	wantAD := string(crypto.AssociatedData(model.AnswersField, result.ID))
	if len(sealer.Calls) != 1 || string(sealer.Calls[0].AssociatedData) != wantAD {
		t.Fatalf("seal calls=%+v want AD %q", sealer.Calls, wantAD)
	}

	// Idempotent replay.
	again, err := svc.SubmitCheckin(ctx, member, key, "v1", answers(1, 1, 1, 0, 1, 0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != result.ID || store.EventCount(model.EventCheckinCompleted) != 1 {
		t.Fatal("idempotent submit must not duplicate")
	}
}

func TestSubmitCheckinCrisisPhonesWhenOpenerFails(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	var logBuf bytes.Buffer
	svc, store, _, crisis := newService(t, clk, &logBuf)
	crisis.Err = errors.New("safety unavailable")
	member := uuid.Must(uuid.NewV7())
	ctx := context.Background()

	// phq_4 at max → crisis via crisis item.
	result, err := svc.SubmitCheckin(ctx, member, uuid.Must(uuid.NewV7()), "v1", answers(0, 0, 0, 3, 0, 0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if result.Flag != model.FlagCrisis {
		t.Fatalf("flag=%s", result.Flag)
	}
	if result.EscalationID != nil {
		t.Fatal("escalation id must be absent when opener fails")
	}
	if len(result.CrisisPhones) != 1 || result.CrisisPhones[0].Number != "+77000000000" {
		t.Fatalf("phones=%+v", result.CrisisPhones)
	}
	if crisis.Len() != 1 {
		t.Fatal("expected one OpenEscalation attempt")
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "CrisisEscalationFailed") {
		t.Fatalf("expected CrisisEscalationFailed log, got %q", logs)
	}
	if strings.Contains(logs, "phq_4") || strings.Contains(logs, `"value"`) {
		t.Fatalf("logs must not contain answers: %q", logs)
	}
	if store.EventCount(model.EventCheckinCompleted) != 1 {
		t.Fatal("checkin must still be stored and emitted")
	}
	data := store.Events()[0].Data.(model.CheckinCompletedData)
	if data.Flag != model.FlagCrisis {
		t.Fatalf("event flag=%s", data.Flag)
	}
}

func TestSubmitCheckinCrisisOpensEscalation(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc, _, _, crisis := newService(t, clk, nil)
	esc := uuid.Must(uuid.NewV7())
	crisis.NextID = esc
	member := uuid.Must(uuid.NewV7())

	result, err := svc.SubmitCheckin(context.Background(), member, uuid.Must(uuid.NewV7()), "v1", answers(3, 3, 3, 3, 0, 0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if result.Flag != model.FlagCrisis || result.EscalationID == nil || *result.EscalationID != esc {
		t.Fatalf("result=%+v", result)
	}
	if crisis.Calls[0].Source != model.EscalationSourceCheckin {
		t.Fatalf("source=%s", crisis.Calls[0].Source)
	}
}

func TestSubmitCheckinRejectsBadAnswers(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc, store, _, _ := newService(t, clk, nil)
	member := uuid.Must(uuid.NewV7())
	short := answers(0, 0, 0, 0, 0, 0, 0, 0)[:7]
	_, err := svc.SubmitCheckin(context.Background(), member, uuid.Must(uuid.NewV7()), "v1", short)
	if !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("err=%v", err)
	}
	if store.EventCount(model.EventCheckinCompleted) != 0 {
		t.Fatal("no event on validation failure")
	}
}

func TestSetScheduleSchedulesAndDisables(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Fatal(err)
	}
	// Wednesday morning Almaty.
	nowLocal := time.Date(2026, 10, 28, 8, 0, 0, 0, loc)
	clk := &fixedClock{t: nowLocal.UTC()}
	svc, store, _, _ := newService(t, clk, nil)
	member := uuid.Must(uuid.NewV7())
	ctx := context.Background()

	sched, err := svc.SetSchedule(ctx, member, 3, 9, true) // Wednesday 09:00
	if err != nil {
		t.Fatal(err)
	}
	if !sched.Enabled || sched.Weekday != 3 || sched.Hour != 9 {
		t.Fatalf("sched=%+v", sched)
	}
	due, ok := store.TimerDue(model.TimerReminder, member)
	if !ok {
		t.Fatal("expected reminder timer")
	}
	want := time.Date(2026, 10, 28, 9, 0, 0, 0, loc)
	if !due.Equal(want) {
		t.Fatalf("due=%v want %v", due, want)
	}

	if _, err := svc.SetSchedule(ctx, member, 3, 9, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.TimerDue(model.TimerReminder, member); ok {
		t.Fatal("disabled schedule must cancel timer")
	}
}

func TestHandleReminderPublishesAndReschedules(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Fatal(err)
	}
	due := time.Date(2026, 10, 28, 9, 0, 0, 0, loc)
	clk := &fixedClock{t: due.UTC()}
	svc, store, _, _ := newService(t, clk, nil)
	member := uuid.Must(uuid.NewV7())
	ctx := context.Background()

	if _, err := svc.SetSchedule(ctx, member, 3, 9, true); err != nil {
		t.Fatal(err)
	}
	// Advance clock to due time and fire.
	clk.t = due.UTC()
	if err := svc.HandleReminder(ctx, member, due); err != nil {
		t.Fatal(err)
	}
	if store.EventCount(model.EventDeliveryRequested) != 1 {
		t.Fatal("expected DeliveryRequested")
	}
	data := store.Events()[len(store.Events())-1].Data.(model.DeliveryRequestedData)
	if data.Template != model.ReminderTemplate || data.Class != model.ReminderClass {
		t.Fatalf("delivery=%+v", data)
	}
	if data.Recipient.MemberID == nil || *data.Recipient.MemberID != member {
		t.Fatalf("recipient=%+v", data.Recipient)
	}
	next, ok := store.TimerDue(model.TimerReminder, member)
	if !ok {
		t.Fatal("expected next reminder")
	}
	wantNext := time.Date(2026, 11, 4, 9, 0, 0, 0, loc)
	if !next.Equal(wantNext) {
		t.Fatalf("next=%v want %v", next, wantNext)
	}
}

func TestGetHistoryAndPractices(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc, store, _, _ := newService(t, clk, nil)
	member := uuid.Must(uuid.NewV7())
	ctx := context.Background()

	store.AddPractice(model.Practice{
		Lang: model.LangRU, TitleKey: "practice.breathe", DurationSeconds: 120, ObjectKey: "ru/breathe.ogg",
	})

	if _, err := svc.SubmitCheckin(ctx, member, uuid.Must(uuid.NewV7()), "v1", answers(0, 0, 0, 0, 0, 4, 4, 0)); err != nil {
		t.Fatal(err)
	}
	points, err := svc.GetHistory(ctx, member, 8)
	if err != nil || len(points) != 1 {
		t.Fatalf("history=%v err=%v", points, err)
	}
	practices, err := svc.ListPractices(ctx, member, model.LangRU)
	if err != nil || len(practices) != 1 {
		t.Fatalf("practices=%v err=%v", practices, err)
	}
	if practices[0].URL != "https://objects.example.invalid/practices/ru/breathe.ogg" {
		t.Fatalf("url=%s", practices[0].URL)
	}
}

func TestUnauthorizedWithoutMember(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc, _, _, _ := newService(t, clk, nil)
	if _, err := svc.GetSchedule(context.Background(), uuid.Nil); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("err=%v", err)
	}
}
