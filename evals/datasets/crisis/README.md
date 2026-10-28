# Crisis evaluation set

Synthetic cases for the level-1 crisis detector (`internal/domains/safety/crisis`; ADR-0017, ADR-0023, `docs/requirements/mvp.md` § 5).

| File | Language of the member |
|---|---|
| `kk.tsv` | Kazakh (with Russian-keyboard spellings and mixed Kazakh-Russian cases) |
| `ru.tsv` | Russian (with mixed Russian-Kazakh cases) |
| `en.tsv` | English (with mixed English-Russian and English-Kazakh cases) |

## Format

The repository has no YAML library, so the set is line-based UTF-8 text:

```
# comment
expected<TAB>lang<TAB>text
```

- `expected`: `crisis` (the detector must fire) or `none` (non-crisis distress or an idiom; a hit is a false positive).
- `lang`: the member's language, `kk`, `ru` or `en`; the detector checks every language, this only sets the order.
- `text`: the message, without tabs or line breaks.

Each file contains direct and indirect statements, typos and obfuscation (repeated letters, spacing, punctuation, Latin look-alikes, Russian keyboard for Kazakh), mixed-language messages and false-positive checks.

## Status

Every case is `status: draft`: written by an agent, not yet reviewed by the project psychologist and a Kazakh native speaker. Draft cases do not gate a release (ADR-0017). When a reviewer approves cases, they move to a separate approved file whose header records `status: approved`, `approved_by` and `approved_at`; only approved files gate.

The unit test `TestDemoLexiconEvalSet` in `internal/domains/safety/crisis` runs `kk.tsv` and `ru.tsv` against its DEMO lexicon: it requires 100% recall on `crisis` cases and logs the false-positive rate on `none` cases. `cmd/birge-eval` (`make eval`) runs every file through the real assistant pipeline with an LLM provider that always fails: every `crisis` case must get the fixed crisis response and an escalation without the model being called; a hit on a `none` case is reported as a false positive and does not fail the run. A real pilot case enters this set only anonymized and with the psychologist's approval; the held-out set never enters the repository.
