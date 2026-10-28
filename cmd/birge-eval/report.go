package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// reportSchema is bumped when a field changes meaning; compare refuses a
// baseline of another schema rather than reporting nonsense drops.
const reportSchema = 1

// knowledgeIndexNone is the knowledge-index version while the eval pipeline
// has no retrieval (offline sets run without TEI and Qdrant).
const knowledgeIndexNone = "none"

// Case results.
const (
	resultPass          = "pass"
	resultFail          = "fail"
	resultFalsePositive = "false_positive"
)

// Report is the JSON report of one run (ADR-0017).
type Report struct {
	Schema                int         `json:"schema"`
	Tool                  string      `json:"tool"`
	Commit                string      `json:"commit"`
	PromptVersion         string      `json:"prompt_version"`
	ModelID               string      `json:"model_id"`
	KnowledgeIndexVersion string      `json:"knowledge_index_version"`
	DatasetVersion        string      `json:"dataset_version"`
	LexiconVersion        string      `json:"lexicon_version"`
	Provider              string      `json:"provider"`
	GeneratedAt           time.Time   `json:"generated_at"`
	Sets                  []SetReport `json:"sets"`
	Totals                Totals      `json:"totals"`
	// Pass is false on any failed gated case or any drop against the baseline.
	Pass  bool   `json:"pass"`
	Drops []Drop `json:"drops,omitempty"`
}

// SetReport is the result of one set.
type SetReport struct {
	Name           string       `json:"name"`
	DatasetVersion string       `json:"dataset_version"`
	Provider       string       `json:"provider"`
	Gate           bool         `json:"gate"`
	Totals         Totals       `json:"totals"`
	Cases          []CaseResult `json:"cases"`
}

// CaseResult is one case (in one mode or sample). It never holds the case
// text or any model output.
type CaseResult struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Lang     string `json:"lang"`
	Expected string `json:"expected"`
	Mode     string `json:"mode,omitempty"`
	Sample   int    `json:"sample,omitempty"`
	Review   string `json:"review"`
	Result   string `json:"result"`
	Reason   string `json:"reason,omitempty"`
}

// compareKey identifies a case result across runs: the content key (stable
// when lines move) plus the mode and sample.
func (c CaseResult) compareKey() string {
	return fmt.Sprintf("%s|%s|%d", c.Key, c.Mode, c.Sample)
}

// Totals counts results. PassRate is passed/cases; false positives count as
// neither passed nor failed.
type Totals struct {
	Cases          int     `json:"cases"`
	Passed         int     `json:"passed"`
	Failed         int     `json:"failed"`
	FalsePositives int     `json:"false_positives"`
	PassRate       float64 `json:"pass_rate"`
}

func (t *Totals) add(result string) {
	t.Cases++
	switch result {
	case resultPass:
		t.Passed++
	case resultFail:
		t.Failed++
	case resultFalsePositive:
		t.FalsePositives++
	}
}

func (t *Totals) rate() {
	if t.Cases > 0 {
		t.PassRate = float64(t.Passed) / float64(t.Cases)
	}
}

// Drop is one regression against the baseline.
type Drop struct {
	Set    string `json:"set"`
	ID     string `json:"id,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Detail string `json:"detail"`
}

// finish fills the totals and Pass.
func (r *Report) finish() {
	r.Totals = Totals{}
	r.Pass = true
	for i := range r.Sets {
		s := &r.Sets[i]
		s.Totals = Totals{}
		for _, c := range s.Cases {
			s.Totals.add(c.Result)
			r.Totals.add(c.Result)
		}
		s.Totals.rate()
		if s.Gate && s.Totals.Failed > 0 {
			r.Pass = false
		}
	}
	r.Totals.rate()
}

// compare returns every drop of cur against base: a case that passed in the
// baseline and does not pass now (a new false positive included), and a set
// whose pass rate fell. Cases new in cur or gone from it are not drops (the
// dataset changed in the same PR, which review sees); a set missing from cur
// is warned about on w.
func compare(base, cur *Report, w io.Writer) []Drop {
	if base.Schema != cur.Schema {
		return []Drop{{Detail: fmt.Sprintf("baseline schema %d, current %d: regenerate the baseline", base.Schema, cur.Schema)}}
	}
	baseSets := map[string]SetReport{}
	for _, s := range base.Sets {
		baseSets[s.Name] = s
	}
	var drops []Drop
	seen := map[string]bool{}
	for _, s := range cur.Sets {
		b, ok := baseSets[s.Name]
		if !ok {
			continue
		}
		seen[s.Name] = true
		was := map[string]string{}
		for _, c := range b.Cases {
			was[c.compareKey()] = c.Result
		}
		for _, c := range s.Cases {
			if prev, ok := was[c.compareKey()]; ok && prev == resultPass && c.Result != resultPass {
				drops = append(drops, Drop{Set: s.Name, ID: c.ID, Mode: c.Mode, Detail: "was pass, now " + c.Result})
			}
		}
		if s.Totals.PassRate+1e-9 < b.Totals.PassRate {
			drops = append(drops, Drop{Set: s.Name, Detail: fmt.Sprintf("pass rate %.4f, baseline %.4f", s.Totals.PassRate, b.Totals.PassRate)})
		}
	}
	for _, s := range base.Sets {
		if !seen[s.Name] {
			_, _ = fmt.Fprintf(w, "birge-eval: warning: set %s is in the baseline but was not run\n", s.Name)
		}
	}
	return drops
}

func readReport(path string) (*Report, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: path from the command line
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("baseline %s: %w", path, err)
	}
	return &r, nil
}

func writeReport(path string, r *Report, stdout io.Writer) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}
	b = append(b, '\n')
	if path == "-" {
		_, err = stdout.Write(b)
		if err != nil {
			return fmt.Errorf("report: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("report: %w", err)
	}
	return nil
}

// summarize prints one line per set and one per failure or drop: IDs and
// reasons only, never case text.
func summarize(r *Report, w io.Writer) {
	for _, s := range r.Sets {
		_, _ = fmt.Fprintf(w, "%s: %d/%d passed, %d failed, %d false positives (dataset %s, provider %s)\n",
			s.Name, s.Totals.Passed, s.Totals.Cases, s.Totals.Failed, s.Totals.FalsePositives, s.DatasetVersion, s.Provider)
		for _, c := range s.Cases {
			if c.Result == resultPass {
				continue
			}
			mode := ""
			if c.Mode != "" {
				mode = " mode=" + c.Mode
			}
			if c.Sample > 0 {
				mode += fmt.Sprintf(" sample=%d", c.Sample)
			}
			_, _ = fmt.Fprintf(w, "  %s %s%s reason=%s\n", c.Result, c.ID, mode, c.Reason)
		}
	}
	for _, d := range r.Drops {
		_, _ = fmt.Fprintf(w, "DROP %s %s %s: %s\n", d.Set, d.ID, d.Mode, d.Detail)
	}
	verdict := "PASS"
	if !r.Pass {
		verdict = "FAIL"
	}
	_, _ = fmt.Fprintf(w, "%s: %d cases, %d failed, %d false positives; prompt %s, model %s, lexicon %s, commit %s\n",
		verdict, r.Totals.Cases, r.Totals.Failed, r.Totals.FalsePositives, r.PromptVersion, r.ModelID, r.LexiconVersion, r.Commit)
}
