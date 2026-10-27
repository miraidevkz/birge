package service_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/assistant/assistanttest"
	"birge/internal/domains/assistant/model"
	"birge/internal/domains/assistant/port"
	"birge/internal/domains/assistant/service"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

var (
	fragA = model.Fragment{ArticleID: uuid.MustParse("019a2f00-0000-7000-8000-00000000000a"), Title: "Benefits (DEMO)", SourceURL: "https://kb.example.invalid/a", Text: "DEMO fragment A"}
	fragB = model.Fragment{ArticleID: uuid.MustParse("019a2f00-0000-7000-8000-00000000000b"), Title: "PMPK (DEMO)", Text: "DEMO fragment B"}
)

type harness struct {
	svc       *service.Service
	store     *assistanttest.MemStore
	consent   *assistanttest.FakeConsent
	detector  *assistanttest.FakeDetector
	opener    *assistanttest.FakeOpener
	profiles  *assistanttest.FakeProfiles
	retriever *assistanttest.FakeRetriever
	llm       *assistanttest.FakeLLM
	clock     *fixedClock
	logs      *bytes.Buffer
	member    uuid.UUID
}

const okJSON = `{"reply":"DEMO reply","crisis":false,"sources":["019a2f00-0000-7000-8000-00000000000a"],"suggest":"psychologist"}`

func newHarness(t *testing.T) *harness {
	t.Helper()
	logs := &bytes.Buffer{}
	h := &harness{
		store:     &assistanttest.MemStore{},
		consent:   &assistanttest.FakeConsent{Granted: true},
		detector:  &assistanttest.FakeDetector{Phrases: []string{"want to die"}},
		opener:    &assistanttest.FakeOpener{NextID: uuid.Must(uuid.NewV7())},
		profiles:  &assistanttest.FakeProfiles{Value: model.Profile{DisplayName: "Zarina Testova", Lang: model.LangEN}},
		retriever: &assistanttest.FakeRetriever{Fragments: []model.Fragment{fragA, fragB}},
		llm:       &assistanttest.FakeLLM{JSON: okJSON},
		clock:     &fixedClock{t: time.Date(2026, 10, 27, 6, 0, 0, 0, time.UTC)},
		logs:      logs,
		member:    uuid.Must(uuid.NewV7()),
	}
	h.svc = &service.Service{
		Store:       h.store,
		Sealer:      &assistanttest.FakeSealer{},
		Consent:     h.consent,
		Crisis:      h.detector,
		Escalations: h.opener,
		Profiles:    h.profiles,
		Retriever:   h.retriever,
		LLM:         h.llm,
		Clock:       h.clock,
		Logger:      slog.New(slog.NewTextHandler(logs, nil)),
		Settings: service.Settings{
			DailyLimit:     3,
			CrisisPhones:   []model.CrisisPhone{{Number: "112", Label: "Emergency services"}},
			NameDictionary: []string{"Arman"},
			Prompt:         service.Prompt{Version: "assistant.test", Text: "TEST prompt"},
		},
	}
	return h
}

func (h *harness) send(t *testing.T, text string) model.Reply {
	t.Helper()
	r, err := h.svc.SendMessage(context.Background(), h.member, uuid.Must(uuid.NewV7()), service.MessageInput{Text: text, Scope: model.ScopeGeneral})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// assertNoTextInLogs checks that no message text, raw or anonymized, reached the logs.
func (h *harness) assertNoTextInLogs(t *testing.T, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if s != "" && strings.Contains(h.logs.String(), s) {
			t.Fatalf("logs contain message text %q", s)
		}
	}
}

func TestHappyPathAnonymizesAndStores(t *testing.T) {
	h := newHarness(t)
	text := "Zarina here, Arman is 7, call +77011234567 about benefits"
	r := h.send(t, text)
	if r.Degraded || r.Crisis || r.Text == nil || *r.Text != "DEMO reply" || r.Suggest != model.SuggestPsychologist {
		t.Fatalf("reply = %+v", r)
	}
	if len(r.Sources) != 1 || r.Sources[0].ArticleID != fragA.ArticleID || r.Sources[0].Title != fragA.Title {
		t.Fatalf("sources = %+v", r.Sources)
	}
	if r.RemainingToday != 2 || r.DisclaimerKey != "" {
		t.Fatalf("remaining=%d disclaimer=%q", r.RemainingToday, r.DisclaimerKey)
	}
	if len(h.llm.Requests) != 1 {
		t.Fatalf("llm calls = %d", len(h.llm.Requests))
	}
	req := h.llm.Requests[0]
	want := "[NAME] here, [NAME] is 7, call [PHONE] about benefits"
	if req.Text != want || h.retriever.Queries[0] != want {
		t.Fatalf("model text = %q, retrieval text = %q", req.Text, h.retriever.Queries[0])
	}
	if req.Lang != model.LangEN || req.SystemPrompt != "TEST prompt" || len(req.Fragments) != 2 {
		t.Fatalf("request = %+v", req)
	}
	turns := h.store.TurnsSnapshot()
	if len(turns) != 2 || turns[0].Role != model.RoleMember || turns[1].Role != model.RoleAssistant || *turns[1].ReplyTo != turns[0].ID {
		t.Fatalf("turns = %+v", turns)
	}
	for _, tr := range turns {
		if bytes.Contains(tr.SealedText, []byte("Zarina")) || bytes.Contains(tr.SealedText, []byte("7011234567")) {
			t.Fatal("stored text is not anonymized")
		}
	}
	if turns[1].ID != r.MessageID {
		t.Fatal("messageId must be the assistant turn")
	}
	h.assertNoTextInLogs(t, text, want, "DEMO reply")
}

func TestConsentMissingStoresNothing(t *testing.T) {
	h := newHarness(t)
	h.consent.Granted = false
	_, err := h.svc.SendMessage(context.Background(), h.member, uuid.Must(uuid.NewV7()), service.MessageInput{Text: "hello", Scope: model.ScopeGeneral})
	if !errors.Is(err, model.ErrConsentRequired) {
		t.Fatalf("err = %v", err)
	}
	if len(h.store.TurnsSnapshot()) != 0 || len(h.llm.Requests) != 0 || len(h.retriever.Queries) != 0 || len(h.opener.Calls) != 0 {
		t.Fatal("nothing may happen without consent")
	}
}

func TestCrisisWithoutConsent(t *testing.T) {
	for name, setup := range map[string]func(h *harness){
		"not granted":         func(h *harness) { h.consent.Granted = false },
		"consent unavailable": func(h *harness) { h.consent.Err = errors.New("privacy down") },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			setup(h)
			r := h.send(t, "I want to die")
			if !r.Crisis || r.Text != nil || r.EscalationID == nil || len(r.CrisisPhones) != 1 || r.RemainingToday != -1 {
				t.Fatalf("reply = %+v", r)
			}
			if len(h.opener.Calls) != 1 || h.opener.Calls[0] != model.EscalationSourceAssistant {
				t.Fatalf("escalations = %v", h.opener.Calls)
			}
			if len(h.llm.Requests) != 0 || len(h.retriever.Queries) != 0 {
				t.Fatal("the model and retrieval must not be called")
			}
			if len(h.store.TurnsSnapshot()) != 0 {
				t.Fatal("nothing may be stored without consent")
			}
			h.assertNoTextInLogs(t, "I want to die")
		})
	}
}

func TestL1CrisisNeverCallsModel(t *testing.T) {
	h := newHarness(t)
	text := "I want to die, Zarina"
	r := h.send(t, text)
	if !r.Crisis || r.Text != nil || r.Degraded || r.EscalationID == nil || *r.EscalationID != h.opener.NextID {
		t.Fatalf("reply = %+v", r)
	}
	if len(r.CrisisPhones) != 1 || r.CrisisPhones[0].Number != "112" {
		t.Fatalf("phones = %+v", r.CrisisPhones)
	}
	if len(h.llm.Requests) != 0 || len(h.retriever.Queries) != 0 {
		t.Fatal("L1 crisis must not reach retrieval or the model")
	}
	if len(h.opener.Calls) != 1 || h.opener.Calls[0] != model.EscalationSourceAssistant {
		t.Fatalf("escalations = %v", h.opener.Calls)
	}
	turns := h.store.TurnsSnapshot()
	if len(turns) != 2 || !turns[0].Crisis || !turns[1].Crisis || turns[1].SealedText != nil {
		t.Fatalf("turns = %+v", turns)
	}
	if bytes.Contains(turns[0].SealedText, []byte("Zarina")) {
		t.Fatal("crisis text stored without anonymization")
	}
	h.assertNoTextInLogs(t, text, "want to die")
}

func TestL1CrisisWorksWhenEverythingElseFails(t *testing.T) {
	h := newHarness(t)
	h.opener.Err = errors.New("safety down")
	h.profiles.Err = errors.New("membership down")
	h.svc.LLM = nil
	h.svc.Sealer = &assistanttest.FakeSealer{SealErr: errors.New("kms down")}
	r := h.send(t, "want to die")
	if !r.Crisis || len(r.CrisisPhones) != 1 || r.EscalationID != nil {
		t.Fatalf("reply = %+v", r)
	}
	if !strings.Contains(h.logs.String(), "CrisisEscalationFailed") {
		t.Fatal("escalation failure must be logged for alerting")
	}
}

func TestL1CrisisBypassesDailyLimit(t *testing.T) {
	h := newHarness(t)
	for range 3 {
		h.send(t, "hello")
	}
	_, err := h.svc.SendMessage(context.Background(), h.member, uuid.Must(uuid.NewV7()), service.MessageInput{Text: "hello", Scope: model.ScopeGeneral})
	if !errors.Is(err, model.ErrDailyLimit) {
		t.Fatalf("err = %v, want daily limit", err)
	}
	r := h.send(t, "I want to die")
	if !r.Crisis || r.RemainingToday != 0 {
		t.Fatalf("crisis over the limit = %+v", r)
	}
}

func TestDailyLimitResetsAtLocalMidnight(t *testing.T) {
	h := newHarness(t)
	// 18:30 UTC is 23:30 in Asia/Almaty (UTC+5).
	h.clock.t = time.Date(2026, 10, 27, 18, 30, 0, 0, time.UTC)
	for range 3 {
		h.send(t, "hello")
	}
	h.clock.t = time.Date(2026, 10, 27, 19, 1, 0, 0, time.UTC) // 00:01 next local day
	if r := h.send(t, "hello"); r.RemainingToday != 2 {
		t.Fatalf("remaining = %d", r.RemainingToday)
	}
}

func TestL2CrisisFromModel(t *testing.T) {
	h := newHarness(t)
	h.llm.JSON = `{"reply":"model text that must not be shown","crisis":true,"sources":[],"suggest":"none"}`
	r := h.send(t, "everything is dark")
	if !r.Crisis || r.Text != nil || r.EscalationID == nil || len(r.CrisisPhones) != 1 {
		t.Fatalf("reply = %+v", r)
	}
	if len(h.opener.Calls) != 1 {
		t.Fatal("L2 must open an escalation")
	}
	turns := h.store.TurnsSnapshot()
	if !turns[1].Crisis || turns[1].SealedText != nil {
		t.Fatal("model text of a crisis reply must not be stored")
	}
}

func TestL2CrisisWinsOverInvalidReply(t *testing.T) {
	h := newHarness(t)
	h.llm.JSON = `{"reply":"","crisis":true,"sources":["not-a-uuid"],"suggest":"bogus"}`
	if r := h.send(t, "hello"); !r.Crisis || r.Degraded {
		t.Fatalf("reply = %+v", r)
	}
}

func TestProviderFailureIsDegraded(t *testing.T) {
	cases := map[string]func(h *harness){
		"timeout":       func(h *harness) { h.llm.Err = &model.ProviderError{Kind: model.FailureTimeout} },
		"rate limit":    func(h *harness) { h.llm.Err = &model.ProviderError{Kind: model.FailureRateLimited} },
		"plain error":   func(h *harness) { h.llm.Err = errors.New("boom") },
		"invalid json":  func(h *harness) { h.llm.JSON = `{"reply":"x",` },
		"not an object": func(h *harness) { h.llm.JSON = `"just text"` },
		"missing field": func(h *harness) { h.llm.JSON = `{"reply":"x","crisis":false,"sources":[]}` },
		"unknown field": func(h *harness) { h.llm.JSON = `{"reply":"x","crisis":false,"sources":[],"suggest":"none","extra":1}` },
		"trailing data": func(h *harness) { h.llm.JSON = `{"reply":"x","crisis":false,"sources":[],"suggest":"none"} {}` },
		"bad suggest":   func(h *harness) { h.llm.JSON = `{"reply":"x","crisis":false,"sources":[],"suggest":"pills"}` },
		"empty reply":   func(h *harness) { h.llm.JSON = `{"reply":"","crisis":false,"sources":[],"suggest":"none"}` },
		"long reply": func(h *harness) {
			h.llm.JSON = `{"reply":"` + strings.Repeat("я", model.MaxReplyChars+1) + `","crisis":false,"sources":[],"suggest":"none"}`
		},
		"no model":     func(h *harness) { h.svc.LLM = nil },
		"profile down": func(h *harness) { h.profiles.Err = errors.New("membership down") },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			setup(h)
			r := h.send(t, "hello")
			if !r.Degraded || r.Crisis || r.Text != nil || r.Suggest != model.SuggestNone || len(r.Sources) != 0 {
				t.Fatalf("reply = %+v", r)
			}
			turns := h.store.TurnsSnapshot()
			if len(turns) != 2 || !turns[1].Degraded || turns[1].SealedText != nil {
				t.Fatalf("turns = %+v", turns)
			}
			if !strings.Contains(h.logs.String(), "AssistantDegraded") {
				t.Fatal("degraded reply must be logged")
			}
		})
	}
}

func TestProfileDownNeverSendsUnanonymizedName(t *testing.T) {
	h := newHarness(t)
	h.profiles.Err = errors.New("membership down")
	h.send(t, "Zarina here")
	if len(h.llm.Requests) != 0 {
		t.Fatal("model must not be asked without the member's name to anonymize")
	}
}

func TestSourcesFilteredToRetrieved(t *testing.T) {
	h := newHarness(t)
	h.llm.JSON = `{"reply":"ok","crisis":false,"sources":["019a2f00-0000-7000-8000-00000000000b","019a2f00-0000-7000-8000-0000000000ff","not-a-uuid","019a2f00-0000-7000-8000-00000000000b"],"suggest":"rights"}`
	r := h.send(t, "hello")
	if len(r.Sources) != 1 || r.Sources[0].ArticleID != fragB.ArticleID {
		t.Fatalf("sources = %+v", r.Sources)
	}
}

func TestRetrievalFailureIsNotFatal(t *testing.T) {
	h := newHarness(t)
	h.retriever.Err = errors.New("vector store down")
	h.llm.JSON = `{"reply":"I do not know","crisis":false,"sources":["019a2f00-0000-7000-8000-00000000000a"],"suggest":"psychologist"}`
	r := h.send(t, "hello")
	if r.Degraded || r.Text == nil || len(r.Sources) != 0 {
		t.Fatalf("reply = %+v", r)
	}
	if len(h.llm.Requests[0].Fragments) != 0 {
		t.Fatal("no fragments expected")
	}
}

func TestRightsScopeAddsDisclaimer(t *testing.T) {
	h := newHarness(t)
	r, err := h.svc.SendMessage(context.Background(), h.member, uuid.Must(uuid.NewV7()), service.MessageInput{Text: "benefits?", Scope: model.ScopeRights, Lang: model.LangKK})
	if err != nil {
		t.Fatal(err)
	}
	if r.DisclaimerKey != service.DefaultRightsDisclaimerKey || h.llm.Requests[0].Lang != model.LangKK || h.llm.Requests[0].Scope != model.ScopeRights {
		t.Fatalf("reply = %+v request = %+v", r, h.llm.Requests[0])
	}
}

func TestIdempotentReplay(t *testing.T) {
	h := newHarness(t)
	key := uuid.Must(uuid.NewV7())
	in := service.MessageInput{Text: "hello Zarina", Scope: model.ScopeGeneral}
	first, err := h.svc.SendMessage(context.Background(), h.member, key, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.svc.SendMessage(context.Background(), h.member, key, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.MessageID != second.MessageID || second.Text == nil || *second.Text != "DEMO reply" {
		t.Fatalf("first = %+v second = %+v", first, second)
	}
	if len(h.llm.Requests) != 1 || len(h.store.TurnsSnapshot()) != 2 {
		t.Fatal("a replay must not call the model or store again")
	}
}

func TestIdempotentReplayOfCrisis(t *testing.T) {
	h := newHarness(t)
	key := uuid.Must(uuid.NewV7())
	in := service.MessageInput{Text: "want to die", Scope: model.ScopeGeneral}
	for range 2 {
		r, err := h.svc.SendMessage(context.Background(), h.member, key, in)
		if err != nil || !r.Crisis || len(r.CrisisPhones) != 1 || r.EscalationID == nil {
			t.Fatalf("reply = %+v err = %v", r, err)
		}
	}
	if len(h.opener.Calls) != 1 {
		t.Fatalf("escalations = %d", len(h.opener.Calls))
	}
}

func TestHistoryIsAnonymizedOldestFirst(t *testing.T) {
	h := newHarness(t)
	h.send(t, "first, Zarina")
	h.clock.t = h.clock.t.Add(time.Minute)
	h.send(t, "want to die") // crisis turns are not sent back to the model
	h.clock.t = h.clock.t.Add(time.Minute)
	h.send(t, "third")
	hist := h.llm.Requests[len(h.llm.Requests)-1].History
	if len(hist) != 2 || hist[0].Role != model.RoleMember || hist[0].Text != "first, [NAME]" || hist[1].Role != model.RoleAssistant || hist[1].Text != "DEMO reply" {
		t.Fatalf("history = %+v", hist)
	}
}

func TestInputValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cases := []struct {
		member, key uuid.UUID
		in          service.MessageInput
		want        error
	}{
		{uuid.Nil, uuid.New(), service.MessageInput{Text: "x", Scope: model.ScopeGeneral}, model.ErrUnauthorized},
		{h.member, uuid.Nil, service.MessageInput{Text: "x", Scope: model.ScopeGeneral}, model.ErrInvalidInput},
		{h.member, uuid.New(), service.MessageInput{Text: "  ", Scope: model.ScopeGeneral}, model.ErrInvalidInput},
		{h.member, uuid.New(), service.MessageInput{Text: strings.Repeat("a", 2001), Scope: model.ScopeGeneral}, model.ErrInvalidInput},
		{h.member, uuid.New(), service.MessageInput{Text: "x", Scope: "medical"}, model.ErrInvalidInput},
		{h.member, uuid.New(), service.MessageInput{Text: "x", Scope: model.ScopeGeneral, Lang: "de"}, model.ErrInvalidInput},
	}
	for i, c := range cases {
		if _, err := h.svc.SendMessage(ctx, c.member, c.key, c.in); !errors.Is(err, c.want) {
			t.Errorf("case %d: err = %v, want %v", i, err, c.want)
		}
	}
}

func TestRateReply(t *testing.T) {
	h := newHarness(t)
	r := h.send(t, "hello")
	ctx := context.Background()
	if err := h.svc.RateReply(ctx, h.member, r.MessageID, true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RateReply(ctx, h.member, r.MessageID, true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RateReply(ctx, h.member, r.MessageID, false); err != nil {
		t.Fatal(err)
	}
	if len(h.store.Events) != 2 || h.store.Events[1].Helped || h.store.Events[1].MessageID != r.MessageID {
		t.Fatalf("events = %+v", h.store.Events)
	}
	if err := h.svc.RateReply(ctx, uuid.Must(uuid.NewV7()), r.MessageID, true); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("other member: err = %v", err)
	}
	memberTurn := h.store.TurnsSnapshot()[0].ID
	if err := h.svc.RateReply(ctx, h.member, memberTurn, true); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("member turn: err = %v", err)
	}
}

func TestRetentionPurge(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.send(t, "old")
	h.clock.t = h.clock.t.AddDate(0, 0, 91)
	h.send(t, "new")
	if err := h.svc.EnsureRetentionPurge(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.EnsureRetentionPurge(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.store.InTx(ctx, func(tx port.Tx) error { return h.svc.HandleRetentionPurgeTx(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	if n := len(h.store.TurnsSnapshot()); n != 2 {
		t.Fatalf("turns after purge = %d", n)
	}
	active := 0
	for _, tm := range h.store.Timers {
		if tm.DoneAt == nil {
			active++
			if !tm.DueAt.Equal(h.clock.t.Add(24 * time.Hour)) {
				t.Fatalf("next purge at %v", tm.DueAt)
			}
		}
	}
	if active != 1 || len(h.store.Timers) != 2 {
		t.Fatalf("timers = %+v", h.store.Timers)
	}
}

func TestLoadPromptDemoPreset(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "presets", "demo", "prompts", "assistant.v1.md")
	p, err := service.LoadPrompt(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != "assistant.v1" || !strings.Contains(p.Text, "DEMO") || !strings.Contains(p.Text, "<member_message>") {
		t.Fatalf("prompt version %q", p.Version)
	}
	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoadPrompt(empty); err == nil {
		t.Fatal("empty prompt must fail")
	}
}
