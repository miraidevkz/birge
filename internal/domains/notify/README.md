# notify

In-app inbox (source of truth), content-free push and SMS delivery, and the critical-notification escalation ladder. Consumes `notify.DeliveryRequested` so other contexts can reach members and trusted contacts without holding channel details (`docs/architecture.md` notify row; residency ADR-0013).

## Owns

| Table (schema `notify`) | Purpose | Personal data |
|---|---|---|
| `notifications` | Inbox / delivery-tracking rows (`template` + `params`; no message body) | `params` may hold non-sensitive template slots; phones are never stored |
| `deliveries` | Push/SMS attempt status per notification and channel | `provider_message_id` only |
| `devices` | Member push endpoints | `push_token` (treat as secret; never log) |

## API

Operations of tag `notify` in `contracts/openapi/birge.yaml`:

| Operation | Rules not in the contract |
|---|---|
| `GET /me/inbox` | Member session only (staff refused). Newest first; cursor pagination; default limit 20, max 50. |
| `POST /me/inbox/{id}/read` | Own notifications only. Idempotent. For `class=critical`, also sets `acknowledged_at` and cancels `notify.CriticalSMS`. |
| `PUT/DELETE /me/devices/{id}` | Own devices only. Upsert on PUT; DELETE idempotent. Push tokens never logged. |

Delivery is not HTTP: JetStream consumer `notify.DeliveryRequested` → `service.HandleDeliveryRequested`, then platform timers call push/SMS handlers after commit.

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| consumes | `notify.DeliveryRequested` v1 | another context needs a delivery | durable consumer; dedup via `platform.processed_events` (consumer name `notify.DeliveryRequested`); one notification per `(request_id, recipient_kind, recipient_ref)` |
| consumes | `privacy.DataDeletionRequested` v1 | account deletion | JetStream durable `notify-deletion` (`wiring.DeletionConsumer`); deduplicated in `platform.processed_events` (consumer `notify.Deletion`) after `service.Deletion.DeleteMember`, which is idempotent and runs in one transaction |
| publishes | none | | |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|
| `notify.Push` | member recipient, new notification (due now; dedup = notification id) | `HandlePush`: send content-free push | skips if delivery already `sent`/`skipped`; at-least-once edge if provider accepts but status update fails |
| `notify.ExternalSMS` | external recipient, new notification (due now) | `HandleExternalSMS`: re-resolve phone, guard, send fixed SMS | same status-based idempotency |
| `notify.CriticalSMS` | member + `class=critical`, after `CriticalSMSAfter` | `HandleCriticalSMS` if still unacknowledged | status + `acknowledged_at`; cancelled on mark-read; critical path ignores daily SMS budget but other guard rejects mark `failed` |

## Service API for other contexts

none (other contexts publish `notify.DeliveryRequested`; they do not call this service directly).

Ports used by this context (wired in `internal/wiring`): `Recipients` (`Resolve` for kind/ref/language only; `PhoneFor` at SMS send time), `PushSender`, `SMSSender`, `SMSLinks` (fills `{link}` at send time: the emergency plan link of an external contact, minted by emergency; optional).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Config.CriticalSMSAfter` | 60s | Wait for acknowledgement before member critical SMS |
| `service.Config.SMSTexts` | required for SMS | `template → language →` neutral fixed body (no message content); unset `BIRGE_SMS_TEXTS` → DEMO preset. `{link}` is replaced at send time through `SMSLinks`, after the guard and right before the provider; no link → the SMS is skipped (`no_link`), a transient error → the timer retries. The link is never stored or logged |
| `guard.Config.AllowedPrefixes` | empty rejects all | E.164 prefix allow-list |
| `guard.Config.PerNumberLimit` / `PerNumberWindow` | 0 disabled / 1h | Per-number rate limit |
| `guard.Config.GlobalLimit` / `GlobalWindow` | 0 disabled / 1m | Process-wide rate limit |
| `guard.Config.DailyBudget` | 0 unlimited | UTC-day send budget (bypassed for critical) |
| see `docs/development.md` § Configuration | | all variables, defaults and which subcommand reads them |

## Invariants

- Consumers idempotent via `processed_events`; unique `(request_id, recipient_kind, recipient_ref)` (database + service).
- `recipient_kind=member`: inbox + push; critical SMS only after acknowledgement timeout. `external`: SMS only, no inbox/push; phone looked up via `PhoneFor` at send time outside any DB transaction, never stored (service + schema).
- `Resolve` returns kind, ref and language only — no identity-provider I/O — so a Kratos outage cannot lose `DeliveryRequested`.
- Push and SMS carry no message content (ADR-0013); SMS bodies from config only.
- `sms/guard` before every SMS; critical uses `AllowCritical` (no daily budget). Guard rejects for critical → `CriticalSMSBlocked` + delivery `failed` (not `skipped`).
- Logs and events: IDs and event names only — never text, phones or push tokens (service + ports).
- Marking critical read acknowledges and cancels CriticalSMS (service).
- Account deletion (AGENTS.md N6): her devices, her inbox (`recipient_kind=member`, ref = her) and every notification whose `params` hold her ID (a plan she sent carries `owner_id`) are deleted with their deliveries; their pending push and SMS timers are cancelled in the same transaction. Nothing is tombstoned: a notification is a message to one recipient, no other record depends on it. One audit entry `notify.member_deleted` (actor system).

## Failure modes

- Database down: inbox/device HTTP and timer handlers fail; JetStream messages are nacked/retried by the consumer.
- Recipients unresolved: `HandleDeliveryRequested` returns `ErrRecipientUnresolved` (message retried); no partial notification for that event until resolve succeeds after `MarkProcessed` — resolve runs before the transaction.
- `PhoneFor` failure (identity provider down): SMS timer handler returns an error; delivery stays pending and retries with backoff — never dropped silently. Empty phone → delivery `skipped`.
- Push/SMS provider down: delivery marked `failed`; timer retry may contact the provider again (at-least-once).
- Guard / missing SMS text: non-critical → `skipped`; critical guard → `failed` + `CriticalSMSBlocked` log; missing text → `skipped`.
- Broker down: outbox from producers lags; this context's consumer stops receiving until JetStream recovers.

## Tests

- Unit: `service/ladder_test.go`, `service/pagination_test.go`, `sms/guard/guard_test.go`, `api/handler` tests via notifytest fakes — ladder, idempotency, guard, cursor.
- Integration: `adapter/integration_test.go` — PostgreSQL + NATS consumer path with `testinfra`.
- Fakes: `notifytest/` (in-memory store, fake push/SMS).
