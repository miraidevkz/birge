package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/assistant/api"
	"birge/internal/domains/assistant/assistanttest"
	"birge/internal/domains/assistant/model"
	"birge/internal/domains/assistant/service"
	"birge/internal/platform/auth"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func newServer(consent bool, llmJSON string) *api.Server {
	return &api.Server{Service: &service.Service{
		Store:       &assistanttest.MemStore{},
		Sealer:      &assistanttest.FakeSealer{},
		Consent:     &assistanttest.FakeConsent{Granted: consent},
		Crisis:      &assistanttest.FakeDetector{Phrases: []string{"want to die"}},
		Escalations: &assistanttest.FakeOpener{NextID: uuid.Must(uuid.NewV7())},
		Profiles:    &assistanttest.FakeProfiles{Value: model.Profile{Lang: model.LangRU}},
		Retriever:   &assistanttest.FakeRetriever{},
		LLM:         &assistanttest.FakeLLM{JSON: llmJSON},
		Clock:       &fixedClock{t: time.Date(2026, 10, 27, 6, 0, 0, 0, time.UTC)},
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service.Settings{
			DailyLimit:   1,
			CrisisPhones: []model.CrisisPhone{{Number: "112", Label: "Emergency services"}},
			Prompt:       service.Prompt{Version: "assistant.test", Text: "TEST"},
		},
	}}
}

func send(t *testing.T, s *api.Server, ctx context.Context, text string) api.SendAssistantMessageResponseObject {
	t.Helper()
	resp, err := s.SendAssistantMessage(ctx, api.SendAssistantMessageRequestObject{
		Params: api.SendAssistantMessageParams{IdempotencyKey: uuid.Must(uuid.NewV7())},
		Body:   &api.SendAssistantMessageJSONRequestBody{Text: text, Scope: api.AssistantMessageInputScopeGeneral},
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func memberCtx() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{MemberID: uuid.Must(uuid.NewV7()), IdentityID: uuid.Must(uuid.NewV7())})
}

func TestRefusesAnonymousAndStaff(t *testing.T) {
	s := newServer(true, `{}`)
	staff := auth.WithPrincipal(context.Background(), auth.Principal{Staff: true, MemberID: uuid.Must(uuid.NewV7())})
	for _, ctx := range []context.Context{context.Background(), staff} {
		resp := send(t, s, ctx, "hello")
		if p, ok := resp.(api.SendAssistantMessagedefaultApplicationProblemPlusJSONResponse); !ok || p.StatusCode != http.StatusUnauthorized {
			t.Fatalf("resp = %#v", resp)
		}
	}
}

func TestConsentRequired(t *testing.T) {
	resp := send(t, newServer(false, `{}`), memberCtx(), "hello")
	p, ok := resp.(api.SendAssistantMessage403ApplicationProblemPlusJSONResponse)
	if !ok || p.Code == nil || *p.Code != "consent_required" {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestDailyLimitAndDegraded(t *testing.T) {
	s := newServer(true, `not json`)
	ctx := memberCtx()
	resp := send(t, s, ctx, "hello")
	ok, isOK := resp.(api.SendAssistantMessage200JSONResponse)
	if !isOK || !ok.Degraded || ok.Text != nil || ok.Suggest != api.AssistantSuggestionNone || ok.Sources == nil {
		t.Fatalf("resp = %#v", resp)
	}
	resp = send(t, s, ctx, "hello")
	if p, isLimit := resp.(api.SendAssistantMessage429ApplicationProblemPlusJSONResponse); !isLimit || *p.Code != "daily_limit" {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestCrisisReply(t *testing.T) {
	resp := send(t, newServer(true, `{}`), memberCtx(), "I want to die")
	ok, isOK := resp.(api.SendAssistantMessage200JSONResponse)
	if !isOK || !ok.Crisis || ok.Text != nil || ok.CrisisPhones == nil || len(*ok.CrisisPhones) != 1 || ok.EscalationId == nil {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestRateUnknownMessage(t *testing.T) {
	s := newServer(true, `{}`)
	resp, err := s.RateAssistantReply(memberCtx(), api.RateAssistantReplyRequestObject{
		MessageId: uuid.Must(uuid.NewV7()), Body: &api.RateAssistantReplyJSONRequestBody{Helped: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := resp.(api.RateAssistantReplydefaultApplicationProblemPlusJSONResponse); !ok || p.StatusCode != http.StatusNotFound {
		t.Fatalf("resp = %#v", resp)
	}
}
