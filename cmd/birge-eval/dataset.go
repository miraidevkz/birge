package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"birge/internal/domains/assistant/model"
)

// Expected outcomes in the dataset files.
const (
	expectCrisis   = "crisis"
	expectNone     = "none"
	expectFallback = "fallback"
	// expectNoLeak is the implicit expectation of an anonymization case.
	expectNoLeak = "no_leak"
)

// Review status of a dataset file (ADR-0017 authoring).
const (
	reviewDraft    = "draft"
	reviewApproved = "approved"
)

// Case is one dataset line.
type Case struct {
	// ID is "<set>/<file>:<line>", for people; Key is stable across line moves.
	ID       string
	Key      string
	Expected string
	Lang     string
	Text     string
	// MustNot is the anonymization token that must not reach the provider.
	MustNot string
	Review  string
}

// Dataset is one set: all *.tsv files of evals/datasets/<name>/.
type Dataset struct {
	Name    string
	Version string // content hash of its files
	Cases   []Case
}

// loadDataset reads every *.tsv file of dir/name in name order.
//
// Formats (see the README of each set):
//   - crisis: expected<TAB>lang<TAB>text, expected crisis or none;
//   - llm-down: expected<TAB>lang<TAB>text, expected crisis or fallback;
//   - anonymization: input<TAB>must-not-contain, the language is the file
//     name (kk.tsv, ru.tsv, en.tsv).
//
// A file is approved only when a header comment (before the first case)
// contains "status: approved"; otherwise its cases are drafts. Every case
// gates make eval; the review status is recorded in the report so a release
// review can tell the two apart.
func loadDataset(root, name string) (Dataset, error) {
	ds := Dataset{Name: name}
	dir := filepath.Join(root, name)
	files, err := filepath.Glob(filepath.Join(dir, "*.tsv"))
	if err != nil {
		return ds, fmt.Errorf("dataset %s: %w", name, err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return ds, fmt.Errorf("dataset %s: no *.tsv files in %s", name, dir)
	}
	h := sha256.New()
	for _, file := range files {
		b, err := os.ReadFile(file) //nolint:gosec // G304: dataset directory from the command line
		if err != nil {
			return ds, fmt.Errorf("dataset %s: %w", name, err)
		}
		base := filepath.Base(file)
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", base, len(b))
		_, _ = h.Write(b)
		cases, err := parseFile(name, base, b)
		if err != nil {
			return ds, err
		}
		ds.Cases = append(ds.Cases, cases...)
	}
	if len(ds.Cases) == 0 {
		return ds, fmt.Errorf("dataset %s: no cases", name)
	}
	ds.Version = "sha256:" + hex.EncodeToString(h.Sum(nil))[:16]
	return ds, nil
}

func parseFile(set, base string, b []byte) ([]Case, error) {
	var cases []Case
	review := reviewDraft
	sc := bufio.NewScanner(bytes.NewReader(b))
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if strings.HasPrefix(raw, "#") {
			if len(cases) == 0 && strings.Contains(raw, "status: approved") {
				review = reviewApproved
			}
			continue
		}
		c, err := parseLine(set, base, raw)
		if err != nil {
			// The line number only: the text stays in the dataset.
			return nil, fmt.Errorf("dataset %s/%s:%d: %w", set, base, line, err)
		}
		c.ID = fmt.Sprintf("%s/%s:%d", set, base, line)
		c.Review = review
		sum := sha256.Sum256([]byte(c.Expected + "\t" + c.Lang + "\t" + c.Text + "\t" + c.MustNot))
		c.Key = hex.EncodeToString(sum[:])[:16]
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("dataset %s/%s: %w", set, base, err)
	}
	return cases, nil
}

func parseLine(set, base, raw string) (Case, error) {
	if set == setAnonymization {
		lang := strings.TrimSuffix(base, ".tsv")
		if !model.LangIsValid(lang) {
			return Case{}, errors.New("file name must be <kk|ru|en>.tsv")
		}
		input, mustNot, ok := strings.Cut(raw, "\t")
		if !ok || strings.TrimSpace(input) == "" || strings.TrimSpace(mustNot) == "" || strings.Contains(mustNot, "\t") {
			return Case{}, errors.New("want input<TAB>must-not-contain")
		}
		return Case{Expected: expectNoLeak, Lang: lang, Text: input, MustNot: mustNot}, nil
	}
	parts := strings.Split(raw, "\t")
	if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
		return Case{}, errors.New("want expected<TAB>lang<TAB>text")
	}
	if !model.LangIsValid(parts[1]) {
		return Case{}, errors.New("lang must be kk, ru or en")
	}
	want := map[string][]string{setCrisis: {expectCrisis, expectNone}, setLLMDown: {expectCrisis, expectFallback}}[set]
	ok := false
	for _, w := range want {
		ok = ok || parts[0] == w
	}
	if !ok {
		return Case{}, fmt.Errorf("expected must be one of %s", strings.Join(want, ", "))
	}
	return Case{Expected: parts[0], Lang: parts[1], Text: parts[2]}, nil
}
