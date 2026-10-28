# Anonymization evaluation set

Synthetic cases for the anonymizer of the assistant pipeline (`internal/domains/assistant/anonymize`; ADR-0013, ADR-0017, `docs/requirements/mvp.md` § 5 step 2). Fake data only.

| File | Language of the member |
|---|---|
| `kk.tsv` | Kazakh (with Russian-keyboard spellings) |
| `ru.tsv` | Russian |
| `en.tsv` | English |

## Format

Line-based UTF-8 text:

```
# comment
input<TAB>must-not-contain
```

- `input`: the member's message, without tabs or line breaks.
- `must-not-contain`: a substring that must not appear in the anonymized text (a phone, IIN, e-mail, address part, document number or name).

Cases cover phones (+7 and 8 forms with spaces, dashes and brackets), IIN, e-mail, street addresses in each language, document numbers and names in their case forms (Russian declension, Kazakh suffixes). Names are matched against the test dictionary `internal/domains/assistant/anonymize/testdata/names.txt`.

## Status

Every case is `status: draft`: written by an agent, not yet reviewed by the project psychologist and a native speaker. Draft cases do not gate a release (ADR-0017). Approved cases move to a separate file whose header records `status: approved`, `approved_by` and `approved_at`.

The unit test `TestAnonymizationEvalSet` runs all three files: every `must-not-contain` must be gone, and an independent check fails on any surviving run of ten or more digits or e-mail. Known gaps (diminutives, names missing from the dictionary, addresses without a keyword) are listed in the package documentation; add a case for every leak found.

`cmd/birge-eval` (`make eval`) runs the same files through the whole assistant pipeline with a recording provider: no request that reaches the provider, including the history of a follow-up turn, may contain the `must-not-contain` token (case-insensitive), a run of ten or more digits or an e-mail.
