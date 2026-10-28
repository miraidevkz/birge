# ADR-0017: AI regression evaluation

Status: Accepted · Date: 2026-10-27

## Context
A change to a prompt, model, knowledge base or pipeline can silently weaken crisis detection or safety behaviour. LLM output is non-deterministic. Requirements: 100% crisis recall (with and without the LLM), no personal data reaching the LLM, signed acceptance by the project psychologist.

## Decision
- **Runner:** our own Go tool (`cmd/birge-eval`) that drives the real pipeline (L1 → anonymize → retrieve → LLM → L2), not the prompt in isolation. Datasets are line-based text files in `evals/datasets/` (`expected<TAB>lang<TAB>text`, no YAML dependency); reports are JSON with prompt version, model ID, knowledge-index version, dataset version and commit, compared with the baseline report from `main`.
- **Datasets:**

  | Set | Check | Gate |
  |---|---|---|
  | `crisis` (kk/ru/en, direct, indirect, typos, mixed language) | L1 alone, deterministic; full pipeline with the LLM, 3 samples per case | 100%; every sample |
  | `anonymization` (phones, IIN, e-mail, inflected names, addresses) | deterministic | zero leaks |
  | `llm-down` | provider mocked to fail/time out | crisis response and escalation always produced |
  | `behavior` (medication, diagnosis, out-of-KB legal, role change and injection, language, length, formal address («вы»), sources ⊂ retrieved) | deterministic checks + judge rubric | zero safety violations; others ≥ 95% and no regression beyond noise |
  | `false-positive` (idioms, non-crisis distress) | trigger rate | threshold set by the psychologist |
  | held-out (outside the repository) | all of the above | release only |

- **Judge:** an LLM grades rubric criteria pass/fail per case (tone, no diagnosis, grounding). The judge model is at least as capable as the assistant model and uses a separate prompt. Agreement with psychologist labels is measured on a labelled sample; a criterion with low agreement moves to human review. The psychologist signs every release report.
- **Schedule:** deterministic sets on every PR. Full LLM sets on PRs that touch prompts, model config, the knowledge base or the pipeline (path filter), nightly as a reduced set through the Batch API, and in full before a release with the held-out set.
- **Authoring:** agents draft synthetic cases by category; the psychologist and a Kazakh native speaker edit and approve them (`status: approved`, `approved_by`, `approved_at`). Only approved cases gate. The held-out set is written by the psychologist and never enters the repository.
- **Data:** synthetic only. A real pilot case enters a dataset only anonymized and with the psychologist's approval.
- **Cost:** budget cap per run; prompt caching for the system prompt; Batch API for nightly runs.

## Consequences
Every bug found in production becomes a dataset case. Changing the assistant model is a normal PR whose acceptance is the eval report.

## Addendum 2026-10-29: runner as built
- **Formats:** `crisis` and `llm-down` use `expected<TAB>lang<TAB>text` (`crisis`/`none`, `crisis`/`fallback`). `anonymization` keeps its existing `input<TAB>must-not-contain` with the language from the file name (`kk.tsv`, `ru.tsv`, `en.tsv`), because the anonymize unit test already reads it.
- **Offline gate:** `make eval` (part of `make go-check`) runs `crisis`, `anonymization` and `llm-down` with fake providers and in-memory storage; every case gates it, drafts included, as the unit tests of the lexicon and anonymizer already did. The report records each case's review status, so the release gate can still count approved cases only.
- **Knowledge index:** the offline pipeline has no retrieval, so the report's `knowledge_index_version` is `none` until a set needs retrieval (`behavior`).
- Usage, report fields and the baseline rule: `docs/development.md` § AI evaluation.
