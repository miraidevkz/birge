package export_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/export"
	"birge/internal/domains/knowledge/knowledgetest"
	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/port"
)

func TestExportPathTraversalRefused(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	w := &export.Writer{Now: func() time.Time { return now }}

	w.Store = &fixedExportStore{items: []model.ExportArticle{{
		Lang: model.LangEN, TopicCode: "demo_ok", ArticleSlug: "../etc", Kind: model.KindSelfhelp,
		Title: "x", Body: "DEMO body", ReviewedAt: now, ExpiresAt: now.Add(time.Hour),
	}}}
	if _, err := w.Export(context.Background(), dir); err == nil {
		t.Fatal("expected path traversal refusal")
	}

	w.Store = &fixedExportStore{items: []model.ExportArticle{{
		Lang: model.LangEN, TopicCode: "demo_ok", ArticleSlug: "a/b", Kind: model.KindSelfhelp,
		Title: "x", Body: "DEMO body", ReviewedAt: now, ExpiresAt: now.Add(time.Hour),
	}}}
	if _, err := w.Export(context.Background(), dir); err == nil {
		t.Fatal("expected separator refusal")
	}

	w.Store = &fixedExportStore{items: []model.ExportArticle{{
		Lang: model.LangEN, TopicCode: "..", ArticleSlug: "ok", Kind: model.KindSelfhelp,
		Title: "x", Body: "DEMO body", ReviewedAt: now, ExpiresAt: now.Add(time.Hour),
	}}}
	if _, err := w.Export(context.Background(), dir); err == nil {
		t.Fatal("expected bad topic slug refusal")
	}
}

func TestExportDraftsNotExported(t *testing.T) {
	store := knowledgetest.NewMemoryStore()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	topicID := uuid.Must(uuid.NewV7())
	articleID := uuid.Must(uuid.NewV7())
	author := uuid.Must(uuid.NewV7())
	if err := store.InTx(context.Background(), func(tx port.Tx) error {
		if _, err := tx.InsertTopic(context.Background(), model.Topic{
			ID: topicID, Kind: model.KindSelfhelp, Code: "demo_burnout", CreatedAt: now,
		}); err != nil {
			return err
		}
		if _, err := tx.InsertArticle(context.Background(), model.Article{
			ID: articleID, TopicID: topicID, Kind: model.KindSelfhelp, Slug: "draft_only", CreatedAt: now,
		}); err != nil {
			return err
		}
		_, _, err := tx.InsertVersion(context.Background(), model.Version{
			ID: uuid.Must(uuid.NewV7()), ArticleID: articleID, Lang: model.LangEN,
			Status: model.StatusDraft, Title: "DEMO draft", Body: "DEMO draft body",
			AuthorID: author, CreatedAt: now, UpdatedAt: now,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	w := &export.Writer{Store: store, Now: func() time.Time { return now }}
	n, err := w.Export(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("exported %d, want 0 drafts", n)
	}
}

func TestExportIdempotentAndGolden(t *testing.T) {
	store := knowledgetest.NewMemoryStore()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	expires := now.Add(180 * 24 * time.Hour)
	topicID := uuid.Must(uuid.NewV7())
	articleID := uuid.Must(uuid.NewV7())
	author := uuid.Must(uuid.NewV7())
	reviewer := uuid.Must(uuid.NewV7())
	src := "https://example.invalid/demo/pmpk-en"
	if err := store.InTx(context.Background(), func(tx port.Tx) error {
		if _, err := tx.InsertTopic(context.Background(), model.Topic{
			ID: topicID, Kind: model.KindRights, Code: "demo_pmpk", CreatedAt: now,
		}); err != nil {
			return err
		}
		if _, err := tx.InsertArticle(context.Background(), model.Article{
			ID: articleID, TopicID: topicID, Kind: model.KindRights, Slug: "apply", CreatedAt: now,
		}); err != nil {
			return err
		}
		vid := uuid.Must(uuid.NewV7())
		if _, _, err := tx.InsertVersion(context.Background(), model.Version{
			ID: vid, ArticleID: articleID, Lang: model.LangEN,
			Status: model.StatusInReview, Title: "DEMO How do I apply to the PMPK?",
			Body:      "DEMO: this is sample text. It is not current law. Check the source.",
			SourceURL: &src, AuthorID: author, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		_, err := tx.PublishVersion(context.Background(), vid, reviewer, now, expires)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	w := &export.Writer{Store: store, Now: func() time.Time { return now }}
	n, err := w.Export(context.Background(), dir)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	path := filepath.Join(dir, "en", "demo_pmpk", "apply.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "apply.en.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden mismatch:\n got:\n%s\nwant:\n%s", got, want)
	}
	if _, err := w.Export(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	got2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got2) != string(want) {
		t.Fatal("second export differed")
	}
	// A file from an earlier run that is no longer published is removed;
	// files outside {lang}/<topic>/*.md are left alone.
	stale := filepath.Join(dir, "en", "demo_pmpk", "retired.md")
	keep := filepath.Join(dir, "README.md")
	for _, f := range []string{stale, keep} {
		if err := os.WriteFile(f, []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Export(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale export kept: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("current export removed: %v", err)
	}
}

func TestYamlEscapesUntrustedFrontMatter(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	src := `https://example.invalid/"trick`
	store := &fixedExportStore{items: []model.ExportArticle{{
		Lang: model.LangEN, TopicCode: "demo_ok", ArticleSlug: "safe_slug", Kind: model.KindSelfhelp,
		Title: "t", Body: "body line\n", SourceURL: &src,
		ReviewedAt: now, ExpiresAt: now.Add(365 * 24 * time.Hour),
	}}}
	dir := t.TempDir()
	w := &export.Writer{Store: store, Now: func() time.Time { return now }}
	if _, err := w.Export(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "en", "demo_ok", "safe_slug.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `source_url: "https://example.invalid/\"trick"`) {
		t.Fatalf("source_url not escaped: %s", s)
	}
	if !strings.Contains(s, `reviewed_by: "psychologist"`) {
		t.Fatalf("reviewed_by missing role: %s", s)
	}
}

type fixedExportStore struct {
	items []model.ExportArticle
}

func (s *fixedExportStore) InTx(_ context.Context, fn func(tx port.Tx) error) error {
	return fn(&fixedExportTx{items: s.items})
}

type fixedExportTx struct {
	items []model.ExportArticle
	port.Tx
}

func (t *fixedExportTx) ListExportVersions(_ context.Context, _ time.Time) ([]model.ExportArticle, error) {
	return t.items, nil
}
