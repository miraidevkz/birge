# assistant

**In short:** The AI conversation partner. Every message first passes the crisis check, then anonymization, and only then may reach the model; when the model is down, the crisis path still works.

The AI assistant of Talk («Поговорить», `docs/requirements/mvp.md` § 4.3) and "Ask in my own words" in My rights (§ 4.7): one fixed pipeline per message (§ 5; ADR-0017, ADR-0023, the `crisis-path` skill) — consent and daily limit, level-1 crisis lexicon, anonymization, retrieval, the model, level-2 crisis flag, persistence of anonymized text only. The crisis response and the escalation never depend on the model (non-negotiable 1); only anonymized text and only with the `ai_crossborder` consent leaves the jurisdiction (N2, ADR-0013).

## Owns

| Table (schema `assistant`) | Purpose | Personal data |
|---|---|---|
| `messages` | Member and assistant turns: role, anonymized text, crisis flag, degraded flag, sources, suggestion, escalation id, rating; idempotency key on member turns; 90-day retention | `text_sealed` sealed (ADR-0009, AD `assistant.messages.text\|<id>`), and only ever anonymized; the rest is plain |

## API

Operations of tag `assistant` in `contracts/openapi/birge.yaml`:

| Operation | Rules not in the contract |
|---|---|
| `POST /me/assistant/messages` | Member session only (`auth.Principal.MemberID`; Nil or staff → 401). Order: L1 on the raw text first → consent (`403 consent_required`, nothing stored; **a crisis message without consent, or when consent cannot be checked, still gets the crisis response and an escalation, with nothing stored and no `remainingToday`**) → replay of a known `Idempotency-Key` (stored reply, no model call, no count) → daily limit (`429 daily_limit`; **a crisis message over the limit still gets the crisis response**) → anonymize → retrieve → model → L2. Provider failure → `200` with `degraded=true`, no text, `suggest=none`. Crisis (L1 or L2) → `crisis=true`, configured phones, `escalationId` when the escalation opened, no text. `remainingToday` counts the calendar day in the deployment zone. Rights scope adds `disclaimerKey`. A replay returns the stored, anonymized reply text. |
| `POST /me/assistant/messages/{messageId}/feedback` | Member only; the message must be the caller's assistant turn (else 404). The last rating wins; `assistant.ReplyRated` is emitted only when the rating changes. |

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `assistant.ReplyRated` v1 | rating stored or changed | outbox, same transaction; payload `message_id`, `helped` only |
| publishes | `assistant.QuestionAnswered` v1 | an exchange is stored (one finished turn) | outbox, same transaction as the exchange; exactly once: only the transaction that inserted the exchange emits it, so a replay or a lost idempotency race emits nothing; nothing for a crisis without consent (nothing is stored). Payload `message_id` (assistant turn), `member_id` (the asker, so analytics can count members behind a cell; it keeps only a keyed pseudonym), `scope`, `source` only; never text or topic. `source`: `knowledge` = the model replied and at least one retrieved article backs it; `no_answer` = retrieval worked (or is disabled) and no retrieved article backs the reply; `crisis` = the fixed crisis response (L1 or L2); `unavailable` = degraded reply (provider failure, no model, profile down) or retrieval failed with an error. On a crisis turn the event is best effort: written in a savepoint, a failure drops it (`AssistantQuestionAnsweredDropped`, Error) and the turn and the crisis response still go out; on any other turn the failure fails the turn |
| consumes | `privacy.DataDeletionRequested` v1 | account deletion | JetStream durable `assistant-deletion` (`wiring.DeletionConsumer`); deduplicated in `platform.processed_events` (consumer `assistant.Deletion`) after `service.Deletion.DeleteMember`, which is idempotent and runs in one transaction |

Escalations are opened through `safety.OpenEscalation` (source `assistant`), which publishes `safety.EscalationOpened`.

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `assistant.RetentionPurge` | `Service.EnsureRetentionPurge` at start-up (dedup key = kind, fixed subject) | deletes one batch of turns older than `RetentionDays`; next run in 24 h, or immediately when the batch was full | singleton by dedup key; deleting expired rows twice is harmless |

## Service API for other contexts

none. Ports implemented by other contexts (wired in `internal/wiring`):

| Port | Implemented by |
|---|---|
| `ConsentChecker` | `privacy.Service.HasConsent` |
| `CrisisDetector` | `safety/crisis` `Lexicon.Detect(...).Crisis` |
| `CrisisOpener` | `safety.Service.OpenEscalation` (source `assistant`) |
| `Profiles` | `membership.Service.Member` (display name, language) |
| `Retriever` | `knowledge/retrieval` via wiring `KnowledgeRetriever` (`NoRetrieval` when TEI/Qdrant unset) |
| `LLM` | `adapter/anthropic` (official Anthropic Go SDK); missing key → nil LLM, degraded replies |
| `Sealer` | platform envelope (`crypto.Envelope`) |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Settings.DailyLimit` | 50 | Member messages per calendar day (§ 4.3) |
| `service.Settings.TimeZone` | `Asia/Almaty` | Zone that defines "day" for the limit; hosts need current tzdata |
| `service.Settings.DefaultLang` | `ru` | Reply language when neither request nor profile has one |
| `service.Settings.CrisisPhones` | empty | Returned with every crisis response (platform `BIRGE_CRISIS_PHONES`) |
| `service.Settings.NameDictionary` | empty | Common names the anonymizer replaces (data; test list in `anonymize/testdata/names.txt`) |
| `service.Settings.Prompt` | required | `service.LoadPrompt(path)`; version = file name. DEMO fallback: `presets/demo/prompts/assistant.v1.md` |
| `service.Settings.HistoryTurns` | 10 | Earlier anonymized turns sent to the model |
| `service.Settings.RetrievalK` | 5 | Fragments requested from retrieval |
| `service.Settings.RightsDisclaimerKey` | `assistant.disclaimer.lawyer` | Locale key added to rights answers |
| `service.Settings.RetentionDays` / `PurgeBatchSize` | 90 / 1000 | Retention (§ 7) and purge batch |
| `anthropic.Config.APIKey` | required | From the secret store; never in the repository |
| `anthropic.Config.Model` | `claude-opus-5-5` | Model ID (logged with every reply) |
| `anthropic.Config.Timeout` | 20 s | Whole call including SDK retries (§ 5 step 7) |
| `anthropic.Config.Effort` / `MaxTokens` / `MaxRetries` | `low` / 4096 / 1 | Adaptive thinking is always on; effort bounds it |
| `anthropic.Config.BaseURL` | SDK default | SDK environment variables are ignored on purpose |

Environment variables: `BIRGE_LLM_MODEL`, `BIRGE_LLM_API_KEY_FILE`, `BIRGE_LLM_BASE_URL`, `BIRGE_ASSISTANT_DAILY_LIMIT`, `BIRGE_ASSISTANT_PROMPT_FILE`, `BIRGE_ASSISTANT_NAMES_FILE` (`docs/development.md` § Configuration).

**The prompt is DEMO and not approved.** `presets/demo/prompts/assistant.v1.md` encodes the behaviour rules of § 5 and the untrusted-data framing; it must be approved by the project psychologist and the product owner before use, and every change runs the evaluation sets (ADR-0017).

## Invariants

- L1 runs on the raw text first, before the consent check, anonymization, retrieval and the model; an L1 hit never calls retrieval or the model (service; `TestL1CrisisNeverCallsModel`).
- The crisis response (phones from configuration, escalation) is returned even when the escalation, the profile, the sealer or the model fails (service; `TestL1CrisisWorksWhenEverythingElseFails`). A daily limit never blocks it (`TestL1CrisisBypassesDailyLimit`).
- Without `ai_crossborder` nothing is stored, retrieved or sent (`TestConsentMissingStoresNothing`); a crisis still gets the escalation and the crisis response (`TestCrisisWithoutConsent`).
- The model sees only anonymized text; when the member's profile (her name) is unavailable the model is not asked (`TestProfileDownNeverSendsUnanonymizedName`).
- Model `crisis=true` wins over the rest of its output; its text is neither returned nor stored (`TestL2CrisisFromModel`, `TestL2CrisisWinsOverInvalidReply`).
- Model output is parsed strictly (four fields, no extras, nothing after); reply 1–2000 characters, suggestion in the enum, sources filtered to the retrieved fragment IDs (model; `model_test.go`, `TestSourcesFilteredToRetrieved`). Any failure → degraded, never a 5xx.
- Only anonymized text is stored, sealed with the member's key; model replies are anonymized again before storage (adapter integration test checks no name in `text_sealed`).
- Logs: IDs, event names, outcome, failure kind, latency, model ID, prompt version. Never message text or anonymized text, never a member ID next to a crisis flag (service tests scan the log output).
- One `Idempotency-Key` = one effect: one model call, one stored exchange, one escalation, one `assistant.QuestionAnswered` (unique index + replay; tested in service and integration tests; `TestQuestionAnsweredExactlyOnce`).
- The crisis response never depends on `assistant.QuestionAnswered`: the escalation and the reply exist before the event is written, and its failure neither fails the crisis turn nor stops it being stored (`TestCrisisResponseWhenQuestionAnsweredFails`, `TestCrisisTurnKeptWhenQuestionAnsweredInsertFailsPostgres`).
- Fragments and member text reach the model as JSON inside `<knowledge_fragments>` / `<member_message>`; '<' and '>' are escaped, so data cannot close its tag (`anthropic_test.go`).
- Account deletion (AGENTS.md non-negotiable 6): every turn of her dialogue is deleted at once, without waiting for the 90-day retention; nothing is tombstoned (the dialogue is hers alone; an escalation it opened is kept by safety as a shell). The crisis path is not touched. One audit entry `assistant.member_deleted` (actor system).

## Failure modes

- AI provider down, slow (> 20 s), rate-limited, refusing or returning invalid JSON: degraded reply; log `AssistantDegraded kind=<timeout|rate_limited|api_error|refusal|truncated|invalid_output|unavailable>`.
- Retrieval down: the model gets no fragments and must say it does not know; log `AssistantRetrievalFailed`.
- Safety down on a crisis: crisis phones still returned; log `CrisisEscalationFailed` at Error (alert on it).
- Outbox write of `assistant.QuestionAnswered` failing: a crisis turn is stored and answered without the event (`AssistantQuestionAnsweredDropped` at Error); a normal message fails with 5xx and nothing is stored.
- Database or sealer down: a normal message fails with 5xx; a crisis message still returns the crisis response (`AssistantCrisisPersistFailed`).
- Membership down: crisis path unaffected; normal message degraded (the name cannot be anonymized).
- Purge failing: platform timer retries with backoff (ADR-0020); rows stay until it succeeds.

## Tests

- Unit: `anonymize/anonymize_test.go` (cases + `TestAnonymizationEvalSet` over `evals/datasets/anonymization/{kk,ru,en}.tsv`: zero phone/IIN/e-mail survivors and every must-not-contain removed), `model/model_test.go`, `service/service_test.go` (pipeline order with fakes, consent, limit, L1, L2, degraded, invalid JSON, sources, replay, history, rating, retention), `api/handler_test.go`.
- Adapter: `adapter/integration_test.go` with `testinfra.Postgres` (exchange, replay, limit, rating outbox payload, `assistant.QuestionAnswered` payloads checked against `contracts/events/assistant.QuestionAnswered.v1.json`, savepoint on a failed event insert, retention purge through `timers.Worker`); `adapter/anthropic/anthropic_test.go` against an `httptest` server emulating the Messages API (no network, no key).
- Evaluation sets: `make eval` (`cmd/birge-eval`, ADR-0017) runs `evals/datasets/{crisis,anonymization,llm-down}/` through this service with in-memory fakes: crisis cases with a provider that always fails (crisis response and escalation, model never called), anonymization cases with a recording provider (no must-not-contain token, digits run or e-mail in any request, history included), llm-down cases in every provider failure mode (crisis response, or the degraded reply and never an error). All cases are **drafts**, not yet reviewed by the project psychologist (see each README). The behaviour set is not built yet.
- Fakes: `assistanttest/`.
