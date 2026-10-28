package adapter_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/directory/adapter"
	"birge/internal/domains/directory/model"
	"birge/internal/platform/outbox"
	"birge/internal/platform/testinfra"
)

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) decide(t *testing.T, kind string, id uuid.UUID, approve bool, reason string) {
	t.Helper()
	if err := e.svc.ApplyModerationDecided(context.Background(), uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: kind, ObjectID: id, Approve: approve, Reason: reason,
	}); err != nil {
		t.Fatal(err)
	}
}

func card(profession string) model.NewSpecialist {
	return model.NewSpecialist{Name: "Fake Clinic", Profession: profession, DistrictID: uuid.New(), Needs: []string{"autism"}}
}

// TestModerationVisibilityPostgres: only approved cards are listed;
// rejections keep their reason; a suggestion replayed with its key returns
// the stored card; only active reviews count.
func TestModerationVisibilityPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.member(true, "Aigul Fakelast")
	key := uuid.Must(uuid.NewV7())
	pending, err := e.svc.SuggestSpecialist(ctx, p, key, card("psychologist"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.SuggestSpecialist(ctx, p, key, card("lawyer"))
	if err != nil || again.ID != pending.ID || again.Profession != "psychologist" {
		t.Fatalf("replay %+v %v", again, err)
	}
	rejected, err := e.svc.SuggestSpecialist(ctx, p, uuid.Must(uuid.NewV7()), card("psychologist"))
	if err != nil {
		t.Fatal(err)
	}
	e.decide(t, model.ModerationKindSpecialist, rejected.ID, false, "duplicate")
	page, err := e.svc.SearchSpecialists(ctx, p, nil, nil, nil, "")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("pending or rejected listed: %+v %v", page.Items, err)
	}
	var reason string
	if err := e.pool.QueryRow(ctx, `SELECT status || ':' || reject_reason FROM directory.specialists WHERE id = $1`, rejected.ID).Scan(&reason); err != nil || reason != "rejected:duplicate" {
		t.Fatalf("rejected row %q %v", reason, err)
	}

	e.decide(t, model.ModerationKindSpecialist, pending.ID, true, "")
	good, err := e.svc.PutReview(ctx, e.member(true, "Dana"), pending.ID, model.NewReview{Rating: 4, Text: "fake review"})
	if err != nil {
		t.Fatal(err)
	}
	bad, err := e.svc.PutReview(ctx, e.member(true, "Sara"), pending.ID, model.NewReview{Rating: 1, Text: "fake review"})
	if err != nil {
		t.Fatal(err)
	}
	e.decide(t, model.ModerationKindReview, good.ID, true, "")
	e.decide(t, model.ModerationKindReview, bad.ID, false, "")
	page, err = e.svc.SearchSpecialists(ctx, p, nil, nil, nil, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("search %+v %v", page.Items, err)
	}
	if it := page.Items[0]; it.ReviewCount != 1 || *it.AverageRating != 4 || len(it.LatestReviews) != 1 || it.LatestReviews[0].ID != good.ID {
		t.Fatalf("card %+v", it)
	}
	if err := e.pool.QueryRow(ctx, `SELECT status || ':' || reject_reason FROM directory.reviews WHERE id = $1`, bad.ID).Scan(&reason); err != nil || reason != "rejected:other" {
		t.Fatalf("rejected review %q %v", reason, err)
	}
	if text, err := e.svc.ReviewSummary(ctx, good.ID); err != nil || text != "fake review" {
		t.Fatalf("review summary %q %v", text, err)
	}
	if _, err := e.svc.SpecialistSummary(ctx, uuid.New()); err == nil {
		t.Fatal("unknown specialist summary")
	}
}

// TestEraseMemberPostgres: her reviews and her unused, non-active cards go;
// active cards and cards others reviewed stay; the audit row is written once.
func TestEraseMemberPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	gone, other := e.member(true, "Aigul"), e.member(true, "Dana")
	unused, err := e.svc.SuggestSpecialist(ctx, gone, uuid.Must(uuid.NewV7()), card("psychologist"))
	if err != nil {
		t.Fatal(err)
	}
	active, err := e.svc.SuggestSpecialist(ctx, gone, uuid.Must(uuid.NewV7()), card("lawyer"))
	if err != nil {
		t.Fatal(err)
	}
	e.decide(t, model.ModerationKindSpecialist, active.ID, true, "")
	reviewed, err := e.svc.SuggestSpecialist(ctx, gone, uuid.Must(uuid.NewV7()), card("speech_therapist"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutReview(ctx, other, reviewed.ID, model.NewReview{Rating: 5, Text: "fake review"}); err != nil {
		t.Fatal(err)
	}
	ownReviewed, err := e.svc.SuggestSpecialist(ctx, gone, uuid.Must(uuid.NewV7()), card("neurologist"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutReview(ctx, gone, ownReviewed.ID, model.NewReview{Rating: 5, Text: "fake review"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutReview(ctx, gone, active.ID, model.NewReview{Rating: 3, Text: "fake review"}); err != nil {
		t.Fatal(err)
	}

	st := &adapter.PostgresStore{Pool: e.pool}
	for range 2 {
		if err := st.EraseMember(ctx, gone.MemberID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM directory.reviews WHERE author_id = $1`, gone.MemberID); n != 0 {
		t.Fatalf("her reviews kept: %d", n)
	}
	for id, want := range map[uuid.UUID]int{unused.ID: 0, ownReviewed.ID: 0, active.ID: 1, reviewed.ID: 1} {
		if n := e.count(t, `SELECT count(*) FROM directory.specialists WHERE id = $1`, id); n != want {
			t.Fatalf("specialist %v: %d rows, want %d", id, n, want)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM directory.reviews WHERE author_id = $1`, other.MemberID); n != 1 {
		t.Fatal("another member's review removed")
	}
	if n := e.count(t, `SELECT count(*) FROM platform.audit_log WHERE action = 'directory.member_deleted' AND object_id = $1`, gone.MemberID); n != 1 {
		t.Fatalf("audit rows %d, want exactly 1", n)
	}
}

func TestSeedDemoIfEmptyPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	system := uuid.New()
	cards := []adapter.DemoSpecialist{
		{IdempotencyKey: uuid.New(), Name: "Fake Demo One", Profession: "psychologist", DistrictID: uuid.New(), Needs: []string{"autism"}, Contacts: "fake contacts"},
		{IdempotencyKey: uuid.New(), Name: "Fake Demo Two", Profession: "lawyer", DistrictID: uuid.New()},
	}
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	if seeded, err := adapter.SeedDemoIfEmpty(ctx, e.pool, system, nil, now); err != nil || seeded {
		t.Fatalf("no cards: %v %v", seeded, err)
	}
	seeded, err := adapter.SeedDemoIfEmpty(ctx, e.pool, system, cards, now)
	if err != nil || !seeded {
		t.Fatalf("seed: %v %v", seeded, err)
	}
	if again, err := adapter.SeedDemoIfEmpty(ctx, e.pool, system, cards, now); err != nil || again {
		t.Fatalf("second seed: %v %v", again, err)
	}
	page, err := e.svc.SearchSpecialists(ctx, e.member(false, "Aigul"), nil, nil, nil, "")
	if err != nil || len(page.Items) != 2 || page.Items[0].Name != "Fake Demo One" || page.Items[1].Needs == nil {
		t.Fatalf("seeded cards %+v %v", page.Items, err)
	}
	// DEMO cards skip moderation: nothing is queued for them.
	if n := e.count(t, `SELECT count(*) FROM platform.outbox`); n != 0 {
		t.Fatalf("seed published %d events", n)
	}
}

func TestModerationConsumerJetStream(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nc, err := nats.Connect(testinfra.NATS(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: "EVENTS", Subjects: []string{"birge.events.>"}}); err != nil {
		t.Fatal(err)
	}
	p := e.member(true, "Aigul")
	sp, err := e.svc.SuggestSpecialist(ctx, p, uuid.Must(uuid.NewV7()), card("psychologist"))
	if err != nil {
		t.Fatal(err)
	}
	cons := &adapter.ModerationConsumer{JS: js, Service: e.svc}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- cons.Run(runCtx) }()
	t.Cleanup(func() { cancel(); <-done })

	data, _ := json.Marshal(model.ModerationDecidedData{ItemID: uuid.New(), Kind: model.ModerationKindSpecialist, ObjectID: sp.ID, Approve: true})
	body, _ := json.Marshal(outbox.Envelope{ID: uuid.Must(uuid.NewV7()), Type: model.EventModerationDecided, Version: 1, OccurredAt: time.Now(), Data: data})
	if _, err := js.Publish(ctx, outbox.Subject(model.EventModerationDecided), body); err != nil {
		t.Fatal(err)
	}
	for range 200 {
		if e.count(t, `SELECT count(*) FROM directory.specialists WHERE id = $1 AND status = 'active'`, sp.ID) == 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if n := e.count(t, `SELECT count(*) FROM directory.specialists WHERE id = $1 AND status = 'active'`, sp.ID); n != 1 {
		t.Fatal("approval not applied through the consumer")
	}

	other, _ := json.Marshal(outbox.Envelope{ID: uuid.New(), Type: "moderation.Other", Version: 1, Data: json.RawMessage(`{}`)})
	if err := cons.HandleMessage(ctx, other); err != nil {
		t.Fatalf("other type: %v", err)
	}
	if err := cons.HandleMessage(ctx, []byte("{")); err == nil {
		t.Fatal("malformed envelope accepted")
	}
	bad, _ := json.Marshal(outbox.Envelope{ID: uuid.New(), Type: model.EventModerationDecided, Version: 1, Data: json.RawMessage(`"x"`)})
	if err := cons.HandleMessage(ctx, bad); err == nil {
		t.Fatal("malformed payload accepted")
	}
}
