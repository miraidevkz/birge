---
name: crisis-path
description: Rules for any change that touches crisis detection, the crisis response, escalations, the AI assistant pipeline or anonymization in Birge (contexts safety, assistant, emergency, wellbeing flags).
---

# Crisis path

This is the part of the system that protects lives. Requirements: `docs/requirements/mvp.ru.md` § 5; ADR-0013, ADR-0017.

## Invariants

1. Level-1 detection (lexicon) runs first, locally, before anonymization and before any network call.
2. The crisis response is fixed text from locale files. The LLM never writes it.
3. Escalation is created and published through the outbox even when the LLM, the knowledge base or the vector store is down.
4. The crisis flow has no feature flag that can switch it off.
5. Phone numbers come from configuration (per country), never from code.

## Pipeline order (do not reorder)

L1 lexicon → anonymize → retrieve → LLM (JSON: reply, crisis, sources, suggest) → L2 (`crisis=true`) → fixed crisis response + escalation → store anonymized text only.

## Tests are mandatory

- Every change runs the crisis set (`evals/datasets/crisis/`, Kazakh and Russian, direct and indirect, typos, mixed language) and must keep 100% recall, including with the LLM disabled (`llm-down` set). Use `cmd/birge-eval`.
- Every change to anonymization runs its set: no phone, IIN, e-mail or name may reach the LLM request.
- A held-out crisis set is kept outside the repository and run before release; do not try to obtain it.
- Add a test case for every bug found. New cases are drafts (`status: draft`) until the psychologist approves them; only approved cases gate.

## Do not

- Do not add the LLM to the critical path of the crisis response.
- Do not log the text of the message that triggered a crisis.
- Do not weaken a threshold or remove a lexicon entry without sign-off recorded in the PR.
