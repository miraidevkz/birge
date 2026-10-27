package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/knowledgetest"
	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/service"
	"birge/internal/platform/auth"
)

func newEnv(t *testing.T) (*service.Service, *knowledgetest.MemoryStore, *knowledgetest.Clock) {
	t.Helper()
	store := knowledgetest.NewMemoryStore()
	clk := &knowledgetest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := &service.Service{
		Store:  store,
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service.Settings{
			RightsTTL:   180 * 24 * time.Hour,
			SelfhelpTTL: 365 * 24 * time.Hour,
		},
	}
	return svc, store, clk
}

func staff(role string) auth.Principal {
	return auth.Principal{IdentityID: uuid.Must(uuid.NewV7()), Staff: true, Roles: []string{role}}
}

func member() auth.Principal {
	return auth.Principal{IdentityID: uuid.Must(uuid.NewV7()), MemberID: uuid.Must(uuid.NewV7())}
}

var topicSeq int

func createTopic(t *testing.T, svc *service.Service, kind string) model.Topic {
	t.Helper()
	topicSeq++
	// Topic codes allow only [a-z_], never digits.
	code := "demo_" + kind + "_" + strings.Repeat("a", topicSeq)
	if len(code) > 40 {
		code = code[:40]
	}
	title := "DEMO topic"
	topic, err := svc.CreateTopic(context.Background(), staff(model.RoleAdmin), kind, code, map[string]*string{
		"ru": &title, "kk": &title, "en": &title,
	})
	if err != nil {
		t.Fatal(err)
	}
	return topic
}

func draftAndSubmit(t *testing.T, svc *service.Service, author auth.Principal, topic model.Topic, lang string, source *string) model.Version {
	t.Helper()
	key := uuid.Must(uuid.NewV7())
	v, err := svc.CreateDraft(context.Background(), author, key, topic.ID, nil, lang, model.Content{
		Title: "DEMO title", Body: "DEMO body text", SourceURL: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err = svc.SubmitForReview(context.Background(), author, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAuthorCannotPublish(t *testing.T) {
	svc, _, _ := newEnv(t)
	topic := createTopic(t, svc, model.KindSelfhelp)
	author := staff(model.RolePsychologist)
	src := "https://example.invalid/x"
	_ = src
	v := draftAndSubmit(t, svc, author, topic, model.LangRU, nil)
	_, err := svc.Publish(context.Background(), author, v.ID)
	if !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("err = %v, want forbidden", err)
	}
}

func TestWrongRoleRefused(t *testing.T) {
	svc, _, _ := newEnv(t)
	topic := createTopic(t, svc, model.KindRights)
	author := staff(model.RoleLawyer)
	source := "https://example.invalid/law"
	v := draftAndSubmit(t, svc, author, topic, model.LangRU, &source)
	psy := staff(model.RolePsychologist)
	_, err := svc.Publish(context.Background(), psy, v.ID)
	if !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("psychologist publishing rights: %v", err)
	}
	_, err = svc.CreateDraft(context.Background(), psy, uuid.Must(uuid.NewV7()), topic.ID, nil, model.LangKK, model.Content{
		Title: "DEMO", Body: "DEMO", SourceURL: &source,
	})
	if !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("psychologist drafting rights: %v", err)
	}
}

func TestOnePublishedPerArticleLangAndNeedsSync(t *testing.T) {
	svc, store, _ := newEnv(t)
	topic := createTopic(t, svc, model.KindSelfhelp)
	author := staff(model.RolePsychologist)
	reviewer := staff(model.RolePsychologist)

	vRU := draftAndSubmit(t, svc, author, topic, model.LangRU, nil)
	pub, err := svc.Publish(context.Background(), reviewer, vRU.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pub.Status != model.StatusPublished {
		t.Fatalf("status = %s", pub.Status)
	}
	articleID := pub.ArticleID

	// Second language of the same article → marks the first needs_sync.
	author2 := staff(model.RolePsychologist)
	key := uuid.Must(uuid.NewV7())
	vEN, err := svc.CreateDraft(context.Background(), author2, key, topic.ID, &articleID, model.LangEN, model.Content{
		Title: "DEMO en", Body: "DEMO body",
	})
	if err != nil {
		t.Fatal(err)
	}
	vEN, err = svc.SubmitForReview(context.Background(), author2, vEN.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(context.Background(), reviewer, vEN.ID); err != nil {
		t.Fatal(err)
	}
	ru, ok := store.Version(vRU.ID)
	if !ok || !ru.NeedsSync {
		t.Fatalf("ru needs_sync = %v ok=%v", ru.NeedsSync, ok)
	}

	// Replace RU: previous published retired, only one published.
	author3 := staff(model.RolePsychologist)
	key2 := uuid.Must(uuid.NewV7())
	vRU2, err := svc.CreateDraft(context.Background(), author3, key2, topic.ID, &articleID, model.LangRU, model.Content{
		Title: "DEMO ru2", Body: "DEMO body 2",
	})
	if err != nil {
		t.Fatal(err)
	}
	vRU2, err = svc.SubmitForReview(context.Background(), author3, vRU2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(context.Background(), reviewer, vRU2.ID); err != nil {
		t.Fatal(err)
	}
	old, ok := store.Version(vRU.ID)
	if !ok || old.Status != model.StatusRetired {
		t.Fatalf("old status = %s ok=%v", old.Status, ok)
	}
	if got := len(store.EventsOfType(model.EventArticlePublished)); got != 3 {
		t.Fatalf("published events = %d, want 3", got)
	}
	if len(store.Audit()) < 2 {
		t.Fatalf("audit entries = %d", len(store.Audit()))
	}
}

func TestPublishSchedulesTimers(t *testing.T) {
	svc, store, clk := newEnv(t)
	topic := createTopic(t, svc, model.KindRights)
	author := staff(model.RoleLawyer)
	reviewer := staff(model.RoleLawyer)
	source := "https://example.invalid/law"
	v := draftAndSubmit(t, svc, author, topic, model.LangRU, &source)
	pub, err := svc.Publish(context.Background(), reviewer, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	due := store.ActiveTimersOf(model.TimerReviewDue)
	exp := store.ActiveTimersOf(model.TimerExpire)
	if len(due) != 1 || len(exp) != 1 {
		t.Fatalf("timers due=%d exp=%d", len(due), len(exp))
	}
	if !due[0].DueAt.Equal(pub.ExpiresAt.Add(-model.ReviewDueLead)) {
		t.Fatalf("review due at %v", due[0].DueAt)
	}
	if !exp[0].DueAt.Equal(*pub.ExpiresAt) {
		t.Fatalf("expire at %v want %v", exp[0].DueAt, *pub.ExpiresAt)
	}
	_ = clk
}

func TestExpireEmitsEvent(t *testing.T) {
	svc, store, _ := newEnv(t)
	topic := createTopic(t, svc, model.KindRights)
	author := staff(model.RoleLawyer)
	reviewer := staff(model.RoleLawyer)
	source := "https://example.invalid/law"
	v := draftAndSubmit(t, svc, author, topic, model.LangRU, &source)
	pub, err := svc.Publish(context.Background(), reviewer, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, commit := store.Tx()
	if err := svc.HandleExpire(context.Background(), tx, pub.ID); err != nil {
		t.Fatal(err)
	}
	commit()
	if len(store.EventsOfType(model.EventArticleExpired)) != 1 {
		t.Fatalf("expired events = %d", len(store.EventsOfType(model.EventArticleExpired)))
	}
}

func TestMemberVisibilityExpiry(t *testing.T) {
	svc, _, clk := newEnv(t)
	ctx := context.Background()
	m := member()

	// Rights: expired → being_rechecked; still listed.
	rightsTopic := createTopic(t, svc, model.KindRights)
	authorL := staff(model.RoleLawyer)
	reviewerL := staff(model.RoleLawyer)
	source := "https://example.invalid/law"
	vR := draftAndSubmit(t, svc, authorL, rightsTopic, model.LangRU, &source)
	pubR, err := svc.Publish(ctx, reviewerL, vR.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Self-help: past expires_at still current.
	helpTopic := createTopic(t, svc, model.KindSelfhelp)
	authorP := staff(model.RolePsychologist)
	reviewerP := staff(model.RolePsychologist)
	vH := draftAndSubmit(t, svc, authorP, helpTopic, model.LangRU, nil)
	pubH, err := svc.Publish(ctx, reviewerP, vH.ID)
	if err != nil {
		t.Fatal(err)
	}

	clk.Advance(200 * 24 * time.Hour) // past rights TTL (180d), inside selfhelp (365d)

	gotR, err := svc.GetArticle(ctx, m, pubR.ArticleID, model.LangRU)
	if err != nil {
		t.Fatal(err)
	}
	if gotR.Status != model.AvailabilityBeingRechecked {
		t.Fatalf("rights status = %s", gotR.Status)
	}
	gotH, err := svc.GetArticle(ctx, m, pubH.ArticleID, model.LangRU)
	if err != nil {
		t.Fatal(err)
	}
	if gotH.Status != model.AvailabilityCurrent {
		t.Fatalf("selfhelp status = %s", gotH.Status)
	}

	frags, err := svc.PublishedFragments(ctx, model.LangRU, model.KindRights)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frags {
		if f.ArticleID == pubR.ArticleID {
			t.Fatal("expired rights must not appear in fragments")
		}
	}
	fragsH, err := svc.PublishedFragments(ctx, model.LangRU, model.KindSelfhelp)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range fragsH {
		if f.ArticleID == pubH.ArticleID {
			found = true
		}
	}
	if !found {
		t.Fatal("non-expired selfhelp missing from fragments")
	}
}

func TestRightsRequireSourceURL(t *testing.T) {
	svc, _, _ := newEnv(t)
	topic := createTopic(t, svc, model.KindRights)
	author := staff(model.RoleLawyer)
	reviewer := staff(model.RoleLawyer)
	v := draftAndSubmit(t, svc, author, topic, model.LangRU, nil)
	_, err := svc.Publish(context.Background(), reviewer, v.ID)
	if !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input", err)
	}
}

func TestRejectReturnsToDraft(t *testing.T) {
	svc, _, _ := newEnv(t)
	topic := createTopic(t, svc, model.KindSelfhelp)
	author := staff(model.RolePsychologist)
	reviewer := staff(model.RolePsychologist)
	v := draftAndSubmit(t, svc, author, topic, model.LangRU, nil)
	out, err := svc.Reject(context.Background(), reviewer, v.ID, model.RejectUnclear)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != model.StatusDraft || out.RejectReason == nil || *out.RejectReason != model.RejectUnclear {
		t.Fatalf("got %+v", out)
	}
}

func TestCreateDraftIdempotent(t *testing.T) {
	svc, _, _ := newEnv(t)
	topic := createTopic(t, svc, model.KindSelfhelp)
	author := staff(model.RolePsychologist)
	key := uuid.Must(uuid.NewV7())
	a, err := svc.CreateDraft(context.Background(), author, key, topic.ID, nil, model.LangRU, model.Content{
		Title: "DEMO", Body: "DEMO body",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateDraft(context.Background(), author, key, topic.ID, nil, model.LangRU, model.Content{
		Title: "DEMO other", Body: "DEMO other",
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || b.Title != "DEMO" {
		t.Fatalf("idempotency failed: %+v vs %+v", a, b)
	}
}
