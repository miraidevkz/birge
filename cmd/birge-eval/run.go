package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"birge/internal/domains/assistant/adapter/anthropic"
	"birge/internal/domains/assistant/anonymize"
	"birge/internal/domains/assistant/port"
	assistantservice "birge/internal/domains/assistant/service"
	"birge/internal/domains/safety/crisis"
	"birge/presets"
)

// Exit codes (see the package documentation).
const (
	exitOK     = 0
	exitFailed = 1
	exitUsage  = 2
)

// Set names, in the order they run.
const (
	setCrisis        = "crisis"
	setAnonymization = "anonymization"
	setLLMDown       = "llm-down"
)

var allSets = []string{setCrisis, setAnonymization, setLLMDown}

// Providers.
const (
	providerFake = "fake"
	providerLive = "live"
)

type options struct {
	datasets   string
	sets       []string
	lexiconDir string
	promptPath string
	namesPath  string
	out        string
	baseline   string
	commit     string
	provider   string
	apiKeyFile string
	model      string
	baseURL    string
	samples    int
	maxCalls   int
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	var sets string
	fsFlags := flag.NewFlagSet("birge-eval", flag.ContinueOnError)
	fsFlags.SetOutput(stderr)
	fsFlags.StringVar(&o.datasets, "datasets", "evals/datasets", "directory with one sub-directory per set")
	fsFlags.StringVar(&sets, "sets", strings.Join(allSets, ","), "comma-separated sets to run")
	fsFlags.StringVar(&o.lexiconDir, "lexicon", "", "crisis lexicon directory (default: the embedded DEMO preset)")
	fsFlags.StringVar(&o.promptPath, "prompt", "", "assistant system prompt file (default: the embedded DEMO preset)")
	fsFlags.StringVar(&o.namesPath, "names", "internal/domains/assistant/anonymize/testdata/names.txt", "name dictionary for the anonymizer")
	fsFlags.StringVar(&o.out, "out", "-", "report file ('-' = stdout)")
	fsFlags.StringVar(&o.baseline, "baseline", "", "baseline report from main; any drop exits 1")
	fsFlags.StringVar(&o.commit, "commit", "", "commit recorded in the report (default: VCS stamp of the binary)")
	fsFlags.StringVar(&o.provider, "provider", providerFake, "fake (offline) or live (manual only)")
	fsFlags.StringVar(&o.apiKeyFile, "api-key-file", "", "live: file holding the provider API key")
	fsFlags.StringVar(&o.model, "model", "", "live: model ID (default: the adapter default)")
	fsFlags.StringVar(&o.baseURL, "base-url", "", "live: provider base URL (default: the SDK default)")
	fsFlags.IntVar(&o.samples, "samples", 3, "live: samples per crisis case (ADR-0017)")
	fsFlags.IntVar(&o.maxCalls, "max-calls", 300, "live: budget cap on provider calls per run")
	if err := fsFlags.Parse(args); err != nil {
		return o, fmt.Errorf("flags: %w", err)
	}
	if fsFlags.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument %q", fsFlags.Arg(0))
	}
	seen := map[string]bool{}
	for _, s := range strings.Split(sets, ",") {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		known := false
		for _, a := range allSets {
			known = known || a == s
		}
		if !known {
			return o, fmt.Errorf("unknown set %q (want %s)", s, strings.Join(allSets, ", "))
		}
		seen[s] = true
		o.sets = append(o.sets, s)
	}
	if len(o.sets) == 0 {
		return o, errors.New("no set selected")
	}
	switch o.provider {
	case providerFake:
	case providerLive:
		if o.apiKeyFile == "" {
			return o, errors.New("-provider live needs -api-key-file")
		}
		if o.samples < 1 || o.maxCalls < 1 {
			return o, errors.New("-samples and -max-calls must be positive")
		}
	default:
		return o, fmt.Errorf("unknown provider %q (want fake or live)", o.provider)
	}
	return o, nil
}

// run is main without os.Exit, so tests drive the whole command.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o, err := parseFlags(args, stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stderr, "birge-eval:", err)
		}
		return exitUsage
	}
	cfg, err := loadPipelineConfig(o)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "birge-eval:", err)
		return exitUsage
	}
	var live *budgetLLM
	if o.provider == providerLive {
		live, err = newLiveProvider(o)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "birge-eval:", err)
			return exitUsage
		}
	}

	rep := &Report{
		Schema:                reportSchema,
		Tool:                  "birge-eval",
		Commit:                commitOf(o.commit),
		PromptVersion:         cfg.prompt.Version,
		ModelID:               fakeModelID,
		KnowledgeIndexVersion: knowledgeIndexNone,
		LexiconVersion:        cfg.lexiconVersion,
		Provider:              o.provider,
		GeneratedAt:           time.Now().UTC().Truncate(time.Second),
	}
	var versions []string
	for _, name := range o.sets {
		ds, err := loadDataset(o.datasets, name)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "birge-eval:", err)
			return exitUsage
		}
		versions = append(versions, name+"="+ds.Version)
		var sr []SetReport
		switch name {
		case setCrisis:
			sr = append(sr, runCrisis(ctx, cfg, ds))
			if live != nil {
				sr = append(sr, runCrisisLive(ctx, cfg, ds, live, o.samples))
			}
		case setAnonymization:
			var provider port.LLM = fixedLLM{JSON: fakeReplyJSON}
			if live != nil {
				provider = live
			}
			sr = append(sr, runAnonymization(ctx, cfg, ds, provider, o.provider))
		case setLLMDown:
			sr = append(sr, runLLMDown(ctx, cfg, ds))
		}
		rep.Sets = append(rep.Sets, sr...)
	}
	rep.DatasetVersion = hashStrings(versions)
	if live != nil {
		rep.ModelID = live.model()
	}
	rep.finish()
	if live != nil && live.exhausted() {
		_, _ = fmt.Fprintf(stderr, "birge-eval: live budget of %d provider calls exhausted; the report is incomplete\n", o.maxCalls)
		return exitUsage
	}
	if o.baseline != "" {
		base, err := readReport(o.baseline)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "birge-eval:", err)
			return exitUsage
		}
		rep.Drops = compare(base, rep, stderr)
		if len(rep.Drops) > 0 {
			rep.Pass = false
		}
	}
	if err := writeReport(o.out, rep, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, "birge-eval:", err)
		return exitUsage
	}
	summarize(rep, stderr)
	if !rep.Pass {
		return exitFailed
	}
	return exitOK
}

// pipelineConfig is what every set needs to build the pipeline.
type pipelineConfig struct {
	lexicon        *crisis.Lexicon
	lexiconVersion string
	prompt         assistantservice.Prompt
	names          []string
}

func loadPipelineConfig(o options) (pipelineConfig, error) {
	var cfg pipelineConfig
	var lexFS fs.FS
	if o.lexiconDir != "" {
		lexFS = os.DirFS(o.lexiconDir)
	} else {
		var err error
		if lexFS, err = presets.CrisisLexicon(); err != nil {
			return cfg, err
		}
	}
	lex, err := crisis.Load(lexFS)
	if err != nil {
		return cfg, err
	}
	cfg.lexicon = lex
	if cfg.lexiconVersion, err = hashTxtFiles(lexFS, "."); err != nil {
		return cfg, fmt.Errorf("lexicon version: %w", err)
	}
	if o.promptPath != "" {
		if cfg.prompt, err = assistantservice.LoadPrompt(o.promptPath); err != nil {
			return cfg, err
		}
	} else {
		version, text, err := presets.AssistantPrompt()
		if err != nil {
			return cfg, err
		}
		cfg.prompt = assistantservice.Prompt{Version: version, Text: strings.TrimSpace(string(text))}
	}
	f, err := os.Open(o.namesPath) //nolint:gosec // G304: path from the command line
	if err != nil {
		return cfg, fmt.Errorf("names: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only
	if cfg.names, err = anonymize.ParseNameList(f); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func newLiveProvider(o options) (*budgetLLM, error) {
	b, err := os.ReadFile(o.apiKeyFile) //nolint:gosec // G304: path from the command line
	if err != nil {
		return nil, errors.New("cannot read -api-key-file") // the path may be sensitive; the key never printed
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return nil, errors.New("-api-key-file is empty")
	}
	client, err := anthropic.New(anthropic.Config{APIKey: key, BaseURL: o.baseURL, Model: o.model})
	if err != nil {
		return nil, fmt.Errorf("live provider: %w", err)
	}
	modelID := o.model
	if modelID == "" {
		modelID = "anthropic-default"
	}
	return &budgetLLM{inner: client, max: o.maxCalls, modelID: modelID}, nil
}

// commitOf returns the flag value, else the VCS revision stamped into the
// binary, else "unknown".
func commitOf(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				return s.Value
			}
		}
	}
	return "unknown"
}

// hashTxtFiles returns a short content hash of the *.txt files in dir of
// fsys (the lexicon version: which entries produced the crisis results).
func hashTxtFiles(fsys fs.FS, dir string) (string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dir, err)
	}
	h := sha256.New()
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".txt" {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", e.Name(), err)
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", e.Name(), len(b))
		_, _ = h.Write(b)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))[:16], nil
}

func hashStrings(ss []string) string {
	sorted := append([]string(nil), ss...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}
