package adapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/assistant/adapter"
	"birge/internal/domains/assistant/assistanttest"
	"birge/internal/domains/assistant/model"
	"birge/internal/domains/assistant/service"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func newService(pg *adapter.PostgresStore, clk *fixedClock, llm *assistanttest.FakeLLM) *service.Service {
	return &service.Service{
		Store:       pg,
		Sealer:      &assistanttest.FakeSealer{},
		Consent:     &assistanttest.FakeConsent{Granted: true},
		Crisis:      &assistanttest.FakeDetector{Phrases: []string{"want to die"}},
		Escalations: &assistanttest.FakeOpener{NextID: uuid.Must(uuid.NewV7())},
		Profiles:    &assistanttest.FakeProfiles{Value: model.Profile{DisplayName: "Zarina", Lang: model.LangEN}},
		Retriever: &assistanttest.FakeRetriever{Fragments: []model.Fragment{{
			ArticleID: uuid.MustParse("019a2f00-0000-7000-8000-00000000000a"), Title: "DEMO", Text: "DEMO",
		}}},
		LLM:    llm,
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service.Settings{
			DailyLimit:     2,
			CrisisPhones:   []model.CrisisPhone{{Number: "112", Label: "Emergency services"}},
			Prompt:         service.Prompt{Version: "assistant.test", Text: "TEST"},
			PurgeBatchSize: 2,
		},
	}
}

func TestExchangeRatingAndLimitPostgres(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	pg := &adapter.PostgresStore{Pool: pool}
	clk := &fixedClock{t: time.Date(2026, 10, 27, 6, 0, 0, 0, time.UTC)}
	llm := &assistanttest.FakeLLM{JSON: `{"reply":"DEMO reply","crisis":false,"sources":["019a2f00-0000-7000-8000-00000000000a"],"suggest":"tea"}`}
	svc := newService(pg, clk, llm)
	member := uuid.Must(uuid.NewV7())
	key := uuid.Must(uuid.NewV7())
	in := service.MessageInput{Text: "hi, Zarina here", Scope: model.ScopeGeneral}

	r, err := svc.SendMessage(ctx, member, key, in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text == nil || len(r.Sources) != 1 || r.Suggest != model.SuggestTea || r.RemainingToday != 1 {
		t.Fatalf("reply = %+v", r)
	}
	replay, err := svc.SendMessage(ctx, member, key, in)
	if err != nil {
		t.Fatal(err)
	}
	if replay.MessageID != r.MessageID || replay.Text == nil || *replay.Text != "DEMO reply" || len(replay.Sources) != 1 || replay.Sources[0].Title != "DEMO" {
		t.Fatalf("replay = %+v", replay)
	}
	if len(llm.Requests) != 1 {
		t.Fatal("replay called the model")
	}

	clk.t = clk.t.Add(time.Minute)
	crisis, err := svc.SendMessage(ctx, member, uuid.Must(uuid.NewV7()), service.MessageInput{Text: "I want to die", Scope: model.ScopeGeneral})
	if err != nil || !crisis.Crisis || crisis.EscalationID == nil {
		t.Fatalf("crisis = %+v err = %v", crisis, err)
	}
	if _, err := svc.SendMessage(ctx, member, uuid.Must(uuid.NewV7()), in); !errors.Is(err, model.ErrDailyLimit) {
		t.Fatalf("err = %v, want daily limit", err)
	}

	var stored, crisisRows int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE crisis_flag) FROM assistant.messages WHERE member_id = $1`, member).Scan(&stored, &crisisRows); err != nil {
		t.Fatal(err)
	}
	if stored != 4 || crisisRows != 2 {
		t.Fatalf("stored=%d crisis=%d", stored, crisisRows)
	}
	var leaked int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM assistant.messages WHERE position('Zarina'::bytea IN text_sealed) > 0`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal("stored text is not anonymized")
	}

	if err := svc.RateReply(ctx, member, r.MessageID, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.RateReply(ctx, member, r.MessageID, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.RateReply(ctx, uuid.Must(uuid.NewV7()), r.MessageID, false); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("other member: err = %v", err)
	}
	var payloads [][]byte
	rows, err := pool.Query(ctx, `SELECT payload FROM platform.outbox WHERE event_type = $1`, model.EventReplyRated)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p []byte
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, p)
	}
	rows.Close()
	if len(payloads) != 1 {
		t.Fatalf("ReplyRated events = %d", len(payloads))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payloads[0], &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 2 || string(raw["helped"]) != "true" || string(raw["message_id"]) != `"`+r.MessageID.String()+`"` {
		t.Fatalf("payload = %s", payloads[0])
	}
}

func TestRetentionPurgeTimerPostgres(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	pg := &adapter.PostgresStore{Pool: pool}
	start := time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: start}
	svc := newService(pg, clk, &assistanttest.FakeLLM{JSON: `{"reply":"ok","crisis":false,"sources":[],"suggest":"none"}`})
	svc.Settings.DailyLimit = 100
	member := uuid.Must(uuid.NewV7())
	for range 3 { // 6 old rows
		if _, err := svc.SendMessage(ctx, member, uuid.Must(uuid.NewV7()), service.MessageInput{Text: "old", Scope: model.ScopeGeneral}); err != nil {
			t.Fatal(err)
		}
	}
	clk.t = start.AddDate(0, 0, 95)
	if _, err := svc.SendMessage(ctx, member, uuid.Must(uuid.NewV7()), service.MessageInput{Text: "new", Scope: model.ScopeGeneral}); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureRetentionPurge(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureRetentionPurge(ctx); err != nil {
		t.Fatal(err)
	}

	worker := &timers.Worker{
		Pool:     pool,
		Clock:    clk,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: (&adapter.TimerHandlers{Service: svc}).Handlers(),
	}
	// Batch size 2: three full batches reschedule immediately, the fourth is
	// empty and schedules tomorrow.
	for i := range 4 {
		fired, err := worker.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !fired {
			t.Fatalf("run %d: no timer fired", i)
		}
	}
	if fired, err := worker.RunOnce(ctx); err != nil || fired {
		t.Fatalf("fired=%v err=%v: next purge must wait a day", fired, err)
	}

	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM assistant.messages`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 2 {
		t.Fatalf("remaining rows = %d, want the 2 recent ones", remaining)
	}
	var active int
	var due time.Time
	if err := pool.QueryRow(ctx,
		`SELECT count(*), max(due_at) FROM platform.timers WHERE kind = $1 AND done_at IS NULL`, model.TimerRetentionPurge,
	).Scan(&active, &due); err != nil {
		t.Fatal(err)
	}
	if active != 1 || !due.Equal(clk.t.Add(24*time.Hour)) {
		t.Fatalf("active=%d due=%v", active, due)
	}
}

// questionAnsweredPayloads returns the outbox payloads of
// assistant.QuestionAnswered in insertion order.
func questionAnsweredPayloads(ctx context.Context, t *testing.T, pool interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) [][]byte {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT payload FROM platform.outbox WHERE event_type = $1 AND event_version = $2 ORDER BY occurred_at, id`,
		model.EventQuestionAnswered, model.EventQuestionAnsweredVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var p []byte
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestQuestionAnsweredOutboxPostgres(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	pg := &adapter.PostgresStore{Pool: pool}
	clk := &fixedClock{t: time.Date(2026, 10, 28, 6, 0, 0, 0, time.UTC)}
	llm := &assistanttest.FakeLLM{JSON: `{"reply":"DEMO reply","crisis":false,"sources":["019a2f00-0000-7000-8000-00000000000a"],"suggest":"none"}`}
	svc := newService(pg, clk, llm)
	svc.Settings.DailyLimit = 100
	member := uuid.Must(uuid.NewV7())

	type turn struct {
		id            uuid.UUID
		scope, source string
	}
	var want []turn
	send := func(text, scope, source string, key uuid.UUID) {
		t.Helper()
		clk.t = clk.t.Add(time.Second)
		r, err := svc.SendMessage(ctx, member, key, service.MessageInput{Text: text, Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		if source != "" {
			want = append(want, turn{r.MessageID, scope, source})
		}
	}
	key := uuid.Must(uuid.NewV7())
	send("benefits?", model.ScopeRights, model.SourceKnowledge, key)
	send("benefits?", model.ScopeRights, "", key) // replay: no second event
	send("I want to die", model.ScopeGeneral, model.SourceCrisis, uuid.Must(uuid.NewV7()))
	llm.Err = errors.New("provider down")
	send("hello", model.ScopeGeneral, model.SourceUnavailable, uuid.Must(uuid.NewV7()))
	llm.Err = nil
	llm.JSON = `{"reply":"I do not know","crisis":false,"sources":[],"suggest":"none"}`
	send("hello", model.ScopeRights, model.SourceNoAnswer, uuid.Must(uuid.NewV7()))

	payloads := questionAnsweredPayloads(ctx, t, pool)
	if len(payloads) != len(want) {
		t.Fatalf("QuestionAnswered events = %d, want %d", len(payloads), len(want))
	}
	for i, p := range payloads {
		testinfra.ValidateEventPayload(t, "assistant.QuestionAnswered.v1.json", p)
		var got model.QuestionAnsweredData
		if err := json.Unmarshal(p, &got); err != nil {
			t.Fatal(err)
		}
		if got != (model.QuestionAnsweredData{MessageID: want[i].id, Scope: want[i].scope, Source: want[i].source}) {
			t.Fatalf("event %d = %s, want %+v", i, p, want[i])
		}
	}
}

func TestCrisisTurnKeptWhenQuestionAnsweredInsertFailsPostgres(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	// Make the outbox reject this one event type inside PostgreSQL, so the
	// failure aborts the statement exactly like a real insert error would.
	if _, err := pool.Exec(ctx, `
CREATE FUNCTION platform.test_reject_question_answered() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.event_type = 'assistant.QuestionAnswered' THEN
    RAISE EXCEPTION 'test: outbox rejects assistant.QuestionAnswered';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER reject_question_answered BEFORE INSERT ON platform.outbox
  FOR EACH ROW EXECUTE FUNCTION platform.test_reject_question_answered();`); err != nil {
		t.Fatal(err)
	}
	pg := &adapter.PostgresStore{Pool: pool}
	clk := &fixedClock{t: time.Date(2026, 10, 28, 6, 0, 0, 0, time.UTC)}
	svc := newService(pg, clk, &assistanttest.FakeLLM{JSON: `{"reply":"ok","crisis":false,"sources":[],"suggest":"none"}`})
	member := uuid.Must(uuid.NewV7())

	r, err := svc.SendMessage(ctx, member, uuid.Must(uuid.NewV7()), service.MessageInput{Text: "I want to die", Scope: model.ScopeGeneral})
	if err != nil || !r.Crisis || len(r.CrisisPhones) != 1 || r.EscalationID == nil {
		t.Fatalf("crisis = %+v err = %v", r, err)
	}
	var crisisTurns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM assistant.messages WHERE member_id = $1 AND crisis_flag`, member).Scan(&crisisTurns); err != nil {
		t.Fatal(err)
	}
	if crisisTurns != 2 {
		t.Fatalf("crisis turns stored = %d, want 2: the savepoint must keep the transaction usable", crisisTurns)
	}

	// A normal turn fails together with its event: no turn without an event.
	clk.t = clk.t.Add(time.Minute)
	if _, err := svc.SendMessage(ctx, member, uuid.Must(uuid.NewV7()), service.MessageInput{Text: "hello", Scope: model.ScopeGeneral}); err == nil {
		t.Fatal("a normal turn must fail when its event cannot be written")
	}
	var all int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM assistant.messages WHERE member_id = $1`, member).Scan(&all); err != nil {
		t.Fatal(err)
	}
	if all != 2 {
		t.Fatalf("stored turns = %d, want only the 2 crisis turns", all)
	}
	if n := len(questionAnsweredPayloads(ctx, t, pool)); n != 0 {
		t.Fatalf("QuestionAnswered events = %d", n)
	}
}
