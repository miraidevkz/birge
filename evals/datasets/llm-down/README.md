# LLM-down evaluation set

Synthetic cases for the assistant pipeline when the AI provider is unavailable (ADR-0017 `llm-down`; non-negotiable 1; `docs/requirements/mvp.md` § 5 step 7). Fake data only.

| File | Language of the member |
|---|---|
| `kk.tsv` | Kazakh |
| `ru.tsv` | Russian |
| `en.tsv` | English |

## Format

Line-based UTF-8 text, like the crisis set:

```
# comment
expected<TAB>lang<TAB>text
```

- `expected`: `crisis` (the fixed crisis response, the configured crisis phones and an escalation must be produced, without calling the model) or `fallback` (an ordinary message: the degraded reply with no model text and `suggest=none`, which the client shows as its fallback text; never an error).
- `lang`: the member's language, `kk`, `ru` or `en`.
- `text`: the message, without tabs or line breaks.

`cmd/birge-eval` (`make eval`) runs every case once per provider failure mode: API error, timeout, rate limit, refusal, invalid JSON output, truncated output and no provider configured. Any miss fails the run.

## Status

Every case is `status: draft`: written by an agent, not yet reviewed by the project psychologist and a native speaker. Draft cases do not gate a release (ADR-0017); approved cases move to a separate file whose header records `status: approved`, `approved_by` and `approved_at`.
