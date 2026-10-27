package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/wellbeing/api"
	"birge/internal/domains/wellbeing/model"
	"birge/internal/domains/wellbeing/service"
	"birge/internal/domains/wellbeing/wellbeingtest"
	"birge/internal/platform/auth"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func testSettings() service.Settings {
	opts := []model.QuestionOption{
		{Value: 0, TextKey: "opt.0"}, {Value: 1, TextKey: "opt.1"},
		{Value: 2, TextKey: "opt.2"}, {Value: 3, TextKey: "opt.3"},
	}
	pssOpts := []model.QuestionOption{
		{Value: 0, TextKey: "pss.0"}, {Value: 1, TextKey: "pss.1"},
		{Value: 2, TextKey: "pss.2"}, {Value: 3, TextKey: "pss.3"}, {Value: 4, TextKey: "pss.4"},
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

func TestHandlersRequireMember(t *testing.T) {
	t.Parallel()
	svc := &service.Service{
		Store:    wellbeingtest.NewMemoryStore(),
		Sealer:   &wellbeingtest.FakeSealer{},
		Crisis:   &wellbeingtest.FakeCrisisOpener{},
		Clock:    &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: testSettings(),
	}
	server := &api.Server{Service: svc}
	ctx := context.Background()

	resp, err := server.GetQuestionnaire(ctx, api.GetQuestionnaireRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := resp.(api.GetQuestionnairedefaultApplicationProblemPlusJSONResponse); !ok || p.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GetQuestionnaire = %#v", resp)
	}

	staff := auth.WithPrincipal(ctx, auth.Principal{Staff: true, MemberID: uuid.Must(uuid.NewV7())})
	schedResp, err := server.GetCheckinSchedule(staff, api.GetCheckinScheduleRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := schedResp.(api.GetCheckinScheduledefaultApplicationProblemPlusJSONResponse); !ok || p.StatusCode != http.StatusUnauthorized {
		t.Fatalf("staff GetCheckinSchedule = %#v", schedResp)
	}
}

func TestSubmitCheckinHappyPath(t *testing.T) {
	t.Parallel()
	svc := &service.Service{
		Store:    wellbeingtest.NewMemoryStore(),
		Sealer:   &wellbeingtest.FakeSealer{},
		Crisis:   &wellbeingtest.FakeCrisisOpener{},
		Clock:    &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: testSettings(),
	}
	server := &api.Server{Service: svc}
	member := uuid.Must(uuid.NewV7())
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{MemberID: member})

	body := &api.CheckinSubmission{
		QuestionnaireVersion: "v1",
		Answers: []struct {
			QuestionId string `json:"questionId"`
			Value      int    `json:"value"`
		}{
			{QuestionId: "phq_1", Value: 0}, {QuestionId: "phq_2", Value: 0},
			{QuestionId: "phq_3", Value: 0}, {QuestionId: "phq_4", Value: 0},
			{QuestionId: "pss_1", Value: 0}, {QuestionId: "pss_2", Value: 0},
			{QuestionId: "pss_3", Value: 0}, {QuestionId: "pss_4", Value: 0},
		},
	}
	resp, err := server.SubmitCheckin(ctx, api.SubmitCheckinRequestObject{
		Params: api.SubmitCheckinParams{IdempotencyKey: uuid.Must(uuid.NewV7())},
		Body:   body,
	})
	if err != nil {
		t.Fatal(err)
	}
	ok, okResp := resp.(api.SubmitCheckin201JSONResponse)
	if !okResp {
		t.Fatalf("resp=%#v", resp)
	}
	if ok.Flag != api.CheckinFlagNone || ok.FeedbackKey == "" {
		t.Fatalf("result=%+v", ok)
	}
}
