package adapter_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/knowledge/adapter"
	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
	"birge/presets"
)

type env struct {
	pool  *pgxpool.Pool
	svc   *service.Service
	clock *fixedClock
}

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testinfra.Postgres(t)
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := &service.Service{
		Store:  &adapter.PostgresStore{Pool: pool},
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service.Settings{
			RightsTTL: 48 * time.Hour, SelfhelpTTL: 72 * time.Hour,
		},
	}
	return &env{pool: pool, svc: svc, clock: clk}
}

func staff(role string) auth.Principal {
	return auth.Principal{IdentityID: uuid.Must(uuid.NewV7()), Staff: true, Roles: []string{role}}
}

func member() auth.Principal {
	return auth.Principal{IdentityID: uuid.Must(uuid.NewV7()), MemberID: uuid.Must(uuid.NewV7())}
}

func TestPublishWorkflowPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	title := "DEMO topic"
	topic, err := e.svc.CreateTopic(ctx, staff(model.RoleAdmin), model.KindRights, "demo_int_pmpk", map[string]*string{
		"ru": &title, "kk": &title, "en": &title,
	})
	if err != nil {
		t.Fatal(err)
	}
	author := staff(model.RoleLawyer)
	reviewer := staff(model.RoleLawyer)
	source := "https://example.invalid/demo"
	v, err := e.svc.CreateDraft(ctx, author, uuid.Must(uuid.NewV7()), topic.ID, nil, model.LangRU, model.Content{
		Title: "DEMO law", Body: "DEMO body", SourceURL: &source,
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err = e.svc.SubmitForReview(ctx, author, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Publish(ctx, author, v.ID); err == nil {
		t.Fatal("author must not publish")
	}
	pub, err := e.svc.Publish(ctx, reviewer, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM platform.outbox WHERE event_type = $1`, model.EventArticlePublished).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("outbox published = %d", n)
	}
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM platform.timers WHERE kind = $1 AND done_at IS NULL`, model.TimerExpire).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expire timers = %d", n)
	}
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM platform.audit_log WHERE action = $1`, model.AuditPublish).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("audit = %d", n)
	}

	// One published per article+lang: second publish retires the first.
	v2, err := e.svc.CreateDraft(ctx, author, uuid.Must(uuid.NewV7()), topic.ID, &pub.ArticleID, model.LangRU, model.Content{
		Title: "DEMO law 2", Body: "DEMO body 2", SourceURL: &source,
	})
	if err != nil {
		t.Fatal(err)
	}
	v2, err = e.svc.SubmitForReview(ctx, author, v2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Publish(ctx, reviewer, v2.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM knowledge.versions WHERE article_id = $1 AND lang = $2 AND status = 'published'`, pub.ArticleID, model.LangRU).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("published count = %d", n)
	}
}

func TestExpireTimerPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	title := "DEMO"
	topic, err := e.svc.CreateTopic(ctx, staff(model.RoleAdmin), model.KindSelfhelp, "demo_int_burn", map[string]*string{"en": &title})
	if err != nil {
		t.Fatal(err)
	}
	author := staff(model.RolePsychologist)
	reviewer := staff(model.RolePsychologist)
	v, err := e.svc.CreateDraft(ctx, author, uuid.Must(uuid.NewV7()), topic.ID, nil, model.LangEN, model.Content{
		Title: "DEMO help", Body: "DEMO body",
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err = e.svc.SubmitForReview(ctx, author, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := e.svc.Publish(ctx, reviewer, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.clock.t = pub.ExpiresAt.Add(time.Second)
	worker := &timers.Worker{
		Pool: e.pool, Clock: e.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: (&adapter.TimerHandlers{Service: e.svc}).Handlers(),
	}
	for i := 0; i < 5; i++ {
		fired, err := worker.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !fired {
			break
		}
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM platform.outbox WHERE event_type = $1`, model.EventArticleExpired).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expired outbox = %d", n)
	}
}

func TestMemberVisibilityPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	title := "DEMO"
	topic, err := e.svc.CreateTopic(ctx, staff(model.RoleLawyer), model.KindRights, "demo_vis", map[string]*string{"ru": &title})
	if err != nil {
		t.Fatal(err)
	}
	author := staff(model.RoleLawyer)
	reviewer := staff(model.RoleLawyer)
	source := "https://example.invalid/x"
	v, err := e.svc.CreateDraft(ctx, author, uuid.Must(uuid.NewV7()), topic.ID, nil, model.LangRU, model.Content{
		Title: "DEMO", Body: "DEMO", SourceURL: &source,
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err = e.svc.SubmitForReview(ctx, author, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := e.svc.Publish(ctx, reviewer, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := member()
	topics, err := e.svc.ListTopics(ctx, m, model.KindRights, model.LangRU)
	if err != nil || len(topics) != 1 {
		t.Fatalf("topics = %v err=%v", topics, err)
	}
	got, err := e.svc.GetArticle(ctx, m, pub.ArticleID, model.LangRU)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.AvailabilityCurrent {
		t.Fatalf("status = %s", got.Status)
	}
	e.clock.t = pub.ExpiresAt.Add(time.Minute)
	got, err = e.svc.GetArticle(ctx, m, pub.ArticleID, model.LangRU)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.AvailabilityBeingRechecked {
		t.Fatalf("status after expiry = %s", got.Status)
	}
	frags, err := e.svc.PublishedFragments(ctx, model.LangRU, model.KindRights)
	if err != nil {
		t.Fatal(err)
	}
	if len(frags) != 0 {
		t.Fatalf("fragments after expiry = %d", len(frags))
	}
}

func TestDemoSeedPostgres(t *testing.T) {
	pool := testinfra.Postgres(t)
	ctx := context.Background()
	k, err := presets.LoadKnowledge()
	if err != nil {
		t.Fatal(err)
	}
	seed := adapter.DemoSeed{}
	for _, topic := range k.Topics {
		seed.Topics = append(seed.Topics, adapter.DemoTopic{
			Kind: topic.Kind, Code: topic.Code,
			TitleKK: topic.Titles["kk"], TitleRU: topic.Titles["ru"], TitleEN: topic.Titles["en"],
		})
	}
	for _, a := range k.Articles {
		da := adapter.DemoArticle{TopicCode: a.TopicCode}
		for _, v := range a.Versions {
			da.Versions = append(da.Versions, adapter.DemoVersion{
				Lang: v.Lang, Title: v.Title, Body: v.Body, SourceURL: v.SourceURL,
			})
		}
		seed.Articles = append(seed.Articles, da)
	}
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	seeded, err := adapter.SeedDemoIfEmpty(ctx, pool, seed, now)
	if err != nil || !seeded {
		t.Fatalf("seeded=%v err=%v", seeded, err)
	}
	seeded2, err := adapter.SeedDemoIfEmpty(ctx, pool, seed, now)
	if err != nil || seeded2 {
		t.Fatalf("second seed seeded=%v err=%v", seeded2, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge.versions WHERE status = 'published'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 9 {
		t.Fatalf("published versions = %d, want ≥9", n)
	}
}
