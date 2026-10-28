package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"birge/presets"
)

var (
	datasetsDir = filepath.Join("..", "..", "evals", "datasets")
	namesFile   = filepath.Join("..", "..", "internal", "domains", "assistant", "anonymize", "testdata", "names.txt")
)

// runEval runs the command with the repository datasets and returns the
// exit code, the report and stderr.
func runEval(t *testing.T, extra ...string) (int, *Report, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "report.json")
	args := append([]string{"-datasets", datasetsDir, "-names", namesFile, "-out", out, "-commit", "test"}, extra...)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	if code == exitUsage {
		t.Fatalf("usage error: %s", stderr.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v (stderr: %s)", err, stderr.String())
	}
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	return code, &rep, stderr.String()
}

func set(t *testing.T, r *Report, name string) SetReport {
	t.Helper()
	for _, s := range r.Sets {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("set %s missing", name)
	return SetReport{}
}

// TestOfflineSetsPass is make eval: every offline set passes with the DEMO
// lexicon and prompt, and the report carries the versions ADR-0017 needs.
func TestOfflineSetsPass(t *testing.T) {
	code, rep, stderr := runEval(t)
	if code != exitOK || !rep.Pass {
		t.Fatalf("exit %d, pass %v:\n%s", code, rep.Pass, stderr)
	}
	if rep.PromptVersion != "assistant.v1" || rep.Commit != "test" || rep.ModelID != fakeModelID ||
		rep.KnowledgeIndexVersion != knowledgeIndexNone || !strings.HasPrefix(rep.DatasetVersion, "sha256:") ||
		!strings.HasPrefix(rep.LexiconVersion, "sha256:") {
		t.Fatalf("report header: %+v", rep)
	}
	langs := map[string]bool{}
	for _, name := range allSets {
		s := set(t, rep, name)
		if s.Totals.Cases == 0 || s.Totals.Failed != 0 || !s.Gate {
			t.Fatalf("%s totals %+v", name, s.Totals)
		}
		for _, c := range s.Cases {
			langs[name+"/"+c.Lang] = true
			if c.Review != reviewDraft {
				t.Fatalf("%s: review %q, all current cases are drafts", c.ID, c.Review)
			}
		}
	}
	for _, name := range allSets {
		for _, l := range []string{"kk", "ru", "en"} {
			if !langs[name+"/"+l] {
				t.Errorf("set %s has no %s cases", name, l)
			}
		}
	}
	// Every llm-down case ran in every failure mode.
	down := set(t, rep, setLLMDown)
	modes := map[string]int{}
	for _, c := range down.Cases {
		modes[c.Mode]++
	}
	if len(modes) != len(llmDownModes) {
		t.Fatalf("llm-down modes %v", modes)
	}
}

// TestBrokenLexiconEntryFailsRun proves the gate: one lexicon entry broken
// (the phrase "хочу умереть" no longer matches) makes crisis cases miss with
// the failing provider, and the run exits 1.
func TestBrokenLexiconEntryFailsRun(t *testing.T) {
	dir := copyDemoLexicon(t)
	path := filepath.Join(dir, "ru.suicide.txt")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(b), "\nхочу умереть\n", "\nхочу пообедать\n", 1)
	if broken == string(b) {
		t.Fatal("entry not found in the DEMO lexicon")
	}
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	code, rep, stderr := runEval(t, "-lexicon", dir, "-sets", "crisis")
	if code != exitFailed || rep.Pass {
		t.Fatalf("exit %d, pass %v: a broken entry must fail the run\n%s", code, rep.Pass, stderr)
	}
	s := set(t, rep, setCrisis)
	if s.Totals.Failed == 0 {
		t.Fatal("no crisis case failed")
	}
	for _, c := range s.Cases {
		if c.Result == resultFail && c.Reason != reasonNotCrisis {
			t.Fatalf("%s: reason %q, want %q", c.ID, c.Reason, reasonNotCrisis)
		}
	}
	if !strings.Contains(stderr, "reason="+reasonNotCrisis) || strings.Contains(stderr, "умереть") {
		t.Fatalf("stderr must name the failing IDs and never the text:\n%s", stderr)
	}
}

// TestBaselineDrop: the same broken lexicon against a good baseline reports
// drops; a run against its own report reports none.
func TestBaselineDrop(t *testing.T) {
	_, good, _ := runEval(t, "-sets", "crisis")
	basePath := filepath.Join(t.TempDir(), "baseline.json")
	b, err := json.Marshal(good)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	code, rep, stderr := runEval(t, "-sets", "crisis", "-baseline", basePath)
	if code != exitOK || len(rep.Drops) != 0 {
		t.Fatalf("same run against itself: exit %d drops %v\n%s", code, rep.Drops, stderr)
	}

	dir := copyDemoLexicon(t)
	if err := os.Remove(filepath.Join(dir, "en.violence.txt")); err != nil {
		t.Fatal(err)
	}
	code, rep, stderr = runEval(t, "-sets", "crisis", "-lexicon", dir, "-baseline", basePath)
	if code != exitFailed || len(rep.Drops) == 0 || !strings.Contains(stderr, "DROP crisis") {
		t.Fatalf("exit %d drops %v\n%s", code, rep.Drops, stderr)
	}
}

func TestCompare(t *testing.T) {
	mk := func(results ...string) *Report {
		r := &Report{Schema: reportSchema, Sets: []SetReport{{Name: setCrisis, Gate: true}}}
		for i, res := range results {
			r.Sets[0].Cases = append(r.Sets[0].Cases, CaseResult{ID: string(rune('a' + i)), Key: string(rune('a' + i)), Result: res})
		}
		r.finish()
		return r
	}
	var w bytes.Buffer
	if d := compare(mk("pass", "pass"), mk("pass", "pass"), &w); len(d) != 0 {
		t.Fatalf("no change: %v", d)
	}
	if d := compare(mk("pass", "pass"), mk("pass", "false_positive"), &w); len(d) != 2 {
		t.Fatalf("new false positive: want case and rate drops, got %v", d)
	}
	if d := compare(mk("fail", "pass"), mk("pass", "pass"), &w); len(d) != 0 {
		t.Fatalf("improvement is not a drop: %v", d)
	}
	other := mk("pass")
	other.Schema = reportSchema + 1
	if d := compare(other, mk("pass"), &w); len(d) != 1 {
		t.Fatalf("schema mismatch: %v", d)
	}
	if d := compare(&Report{Schema: reportSchema, Sets: []SetReport{{Name: setLLMDown}}}, mk("pass"), &w); len(d) != 0 || !strings.Contains(w.String(), "not run") {
		t.Fatalf("missing set: %v %q", d, w.String())
	}
}

// TestAnonymizationLeakDetected proves the recording provider catches a leak:
// without the name dictionary, names reach the provider and the run fails.
func TestAnonymizationLeakDetected(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "names.txt")
	if err := os.WriteFile(empty, []byte("# no names\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, rep, stderr := runEval(t, "-sets", "anonymization", "-names", empty)
	if code != exitFailed {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	leaks := 0
	for _, c := range set(t, rep, setAnonymization).Cases {
		if c.Reason == reasonLeakMustNot {
			leaks++
		}
	}
	if leaks == 0 {
		t.Fatal("no leak reported")
	}
}

func TestDatasetErrors(t *testing.T) {
	cases := map[string]struct{ set, file, body string }{
		"crisis two fields":     {setCrisis, "ru.tsv", "crisis\tхочу\n"},
		"crisis bad expected":   {setCrisis, "ru.tsv", "fallback\tru\tтекст\n"},
		"llm-down bad expected": {setLLMDown, "ru.tsv", "none\tru\tтекст\n"},
		"bad lang":              {setCrisis, "ru.tsv", "crisis\tde\ttext\n"},
		"anon file name":        {setAnonymization, "de.tsv", "text\ttoken\n"},
		"anon no token":         {setAnonymization, "ru.tsv", "text only\n"},
		"empty":                 {setCrisis, "ru.tsv", "# only a comment\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, tc.set)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadDataset(root, tc.set); err == nil {
				t.Fatal("want error")
			}
		})
	}
	if _, err := loadDataset(t.TempDir(), setCrisis); err == nil {
		t.Fatal("missing directory: want error")
	}
}

func TestApprovedHeader(t *testing.T) {
	cases, err := parseFile(setCrisis, "ru.tsv", []byte("# status: approved, approved_by: x\ncrisis\tru\tтекст\n"))
	if err != nil || len(cases) != 1 || cases[0].Review != reviewApproved || cases[0].ID != "crisis/ru.tsv:2" {
		t.Fatalf("%v %+v", err, cases)
	}
}

func TestFlags(t *testing.T) {
	var w bytes.Buffer
	for _, args := range [][]string{
		{"-sets", "behavior"},
		{"-provider", "live"},
		{"-provider", "other"},
		{"extra"},
	} {
		if code := run(context.Background(), args, &w, &w); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
}

func copyDemoLexicon(t *testing.T) string {
	t.Helper()
	src, err := presets.CrisisLexicon()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := fs.ReadFile(src, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
