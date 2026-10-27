// Package export writes published knowledge versions to a deterministic
// markdown tree for the public repository (ADR-0022). Drafts, in-review and
// retired versions are never exported; expired rights are skipped. reviewed_by
// is the reviewer role, never a staff id or name.
package export

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/port"
)

const fileMode = 0o644
const dirMode = 0o755

// Store loads published versions for export.
type Store interface {
	InTx(ctx context.Context, fn func(tx port.Tx) error) error
}

// Writer writes the current published knowledge base under outDir as
// {lang}/{topic-slug}/{article-slug}.md with YAML front matter.
type Writer struct {
	Store Store
	Now   func() time.Time
}

// Export writes every exportable published version under outDir. Output is
// deterministic: sorted paths, stable front-matter formatting, no run
// timestamps. A rerun without content changes produces no diff.
func (w *Writer) Export(ctx context.Context, outDir string) (int, error) {
	if strings.TrimSpace(outDir) == "" {
		return 0, fmt.Errorf("knowledge export: out dir required")
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return 0, fmt.Errorf("knowledge export: resolve out: %w", err)
	}
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}
	var items []model.ExportArticle
	if err := w.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		items, err = tx.ListExportVersions(ctx, now)
		return err
	}); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(absOut, dirMode); err != nil {
		return 0, fmt.Errorf("knowledge export: mkdir: %w", err)
	}
	n := 0
	written := make(map[string]bool, len(items))
	for _, a := range items {
		path, err := writeArticle(absOut, a)
		if err != nil {
			return n, err
		}
		written[path] = true
		n++
	}
	if err := pruneStale(absOut, written); err != nil {
		return n, err
	}
	return n, nil
}

// pruneStale removes exported .md files that this run did not write, so a
// retired or expired version does not stay in the public tree. It touches
// only <out>/{lang}/<topic>/*.md, never anything else under out.
func pruneStale(outDir string, written map[string]bool) error {
	for _, lang := range []string{model.LangKK, model.LangRU, model.LangEN} {
		files, err := filepath.Glob(filepath.Join(outDir, lang, "*", "*.md"))
		if err != nil {
			return fmt.Errorf("knowledge export: list %s: %w", lang, err)
		}
		for _, f := range files {
			if written[f] {
				continue
			}
			if err := os.Remove(f); err != nil {
				return fmt.Errorf("knowledge export: remove stale: %w", err)
			}
		}
	}
	return nil
}

func writeArticle(outDir string, a model.ExportArticle) (string, error) {
	if !model.SlugSafe(a.TopicCode) || !model.SlugSafe(a.ArticleSlug) {
		return "", fmt.Errorf("%w: unsafe topic or article slug", model.ErrInvalidInput)
	}
	if !model.LangIsValid(a.Lang) {
		return "", fmt.Errorf("%w: lang", model.ErrInvalidInput)
	}
	dir := filepath.Join(outDir, a.Lang, a.TopicCode)
	// Refuse any path that escapes outDir (defence in depth after slug checks).
	rel, err := filepath.Rel(outDir, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%w: path escape", model.ErrInvalidInput)
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return "", fmt.Errorf("knowledge export: mkdir article: %w", err)
	}
	path := filepath.Join(dir, a.ArticleSlug+".md")
	relFile, err := filepath.Rel(outDir, path)
	if err != nil || strings.HasPrefix(relFile, "..") {
		return "", fmt.Errorf("%w: path escape", model.ErrInvalidInput)
	}
	body, err := renderMarkdown(a)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(body), fileMode); err != nil {
		return "", fmt.Errorf("knowledge export: write: %w", err)
	}
	return path, nil
}

func renderMarkdown(a model.ExportArticle) (string, error) {
	if !utf8.ValidString(a.Body) {
		return "", fmt.Errorf("%w: body not utf-8", model.ErrInvalidInput)
	}
	source := ""
	if a.SourceURL != nil {
		source = *a.SourceURL
	}
	role := model.ReviewerRoleFor(a.Kind)
	if role == "" {
		return "", fmt.Errorf("%w: kind", model.ErrInvalidInput)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: " + yamlQuote(a.Title) + "\n")
	b.WriteString("topic: " + yamlQuote(a.TopicCode) + "\n")
	b.WriteString("source_url: " + yamlQuote(source) + "\n")
	b.WriteString("reviewed_by: " + yamlQuote(role) + "\n")
	b.WriteString("reviewed_at: " + yamlQuote(a.ReviewedAt.UTC().Format(time.RFC3339)) + "\n")
	b.WriteString("expires_at: " + yamlQuote(a.ExpiresAt.UTC().Format(time.RFC3339)) + "\n")
	b.WriteString("---\n\n")
	b.WriteString(a.Body)
	if !strings.HasSuffix(a.Body, "\n") {
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// yamlQuote escapes an untrusted scalar as a double-quoted YAML string.
func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
