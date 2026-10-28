// Command birge-eval is the AI regression runner of ADR-0017. It drives the
// line-based datasets in evals/datasets/ through the real assistant pipeline
// (assistant service: L1 lexicon → consent → anonymize → retrieve → model →
// L2 → persistence; safety service: escalation) and writes a JSON report that
// can be compared with the baseline report from main.
//
// Storage is in memory (assistanttest, safetytest fakes), so no database,
// message bus or network is needed. The crisis lexicon, the escalation path
// and the pipeline order are the production code: wiring.LexiconCrisis and
// wiring.CrisisOpener are the same adapters the API server uses.
//
// # Sets
//
//   - crisis: every case runs with an LLM provider that always fails. A
//     crisis case passes only when the member gets the fixed crisis response
//     (crisis flag, configured phones, no model text), an escalation is
//     opened with its backup timer, and the model was never called. This is
//     the executable proof of non-negotiable 1. A hit on a "none" case is a
//     false positive: reported, not a failure (ADR-0017: the threshold is the
//     psychologist's).
//   - anonymization: every case runs with a recording provider. It passes
//     when the request reached the provider and no request (the message and
//     the history of a follow-up turn) contains the case's must-not-contain
//     token, a run of ten or more digits, or an e-mail address
//     (non-negotiable 2).
//   - llm-down: every case runs once per provider failure mode. Crisis cases
//     must still get the crisis response and an escalation; ordinary cases
//     must get the degraded fallback reply and never an error.
//
// # Providers
//
// -provider fake (the default, used by make eval and CI) never opens a
// network connection. -provider live sends the anonymization set and the
// crisis set (with -samples per case) to the real model through the
// production Anthropic adapter, with the key read from -api-key-file; it is
// for manual runs only, is capped by -max-calls, and still runs the crisis
// and llm-down checks with the failing provider first.
//
// # Exit status
//
// 0: every gated case passed and nothing dropped against -baseline.
// 1: a crisis, anonymization or llm-down miss, or a drop against the
// baseline. 2: bad flags, an unreadable dataset or configuration, or the
// live budget was exhausted.
//
// The report and the logs carry case IDs (file:line) and outcomes only,
// never the case text or the model output: datasets are synthetic, but a
// held-out or live run must not copy text into CI artifacts.
package main

import (
	"context"
	"os"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
