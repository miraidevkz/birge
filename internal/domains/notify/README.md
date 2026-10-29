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

Delivery is not HTTP: JetStream durable `notify-delivery-requested` (processed-events name `notify.DeliveryRequested`) → `service.HandleDeliveryRequested`, then platform timers call push/SMS handlers after commit.

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

Other contexts publish `notify.DeliveryRequested`. One exception, for a secret that must never be stored:

| Function | Callers may rely on |
|---|---|
| `service.Service.SendSignInCode(ctx, phoneE164, code)` | Synchronous sign-in code SMS for membership's Kratos courier hook (wired in `internal/wiring.SignInCodes`). Never touches the store, the outbox or a timer, so the code is kept only in memory and in the provider request. Text fixed as `SignInCodeText` (`Birge: <code>`); code must be 4 to 10 digits (`model.ErrInvalidInput`). `SignInGuard` first: refusal → `ErrSMSRefused` (wraps the guard reason). Provider errors returned as they are (`port.ErrPermanent` or transient); no retry here, Kratos retries. Logs a random `sms_id` and the outcome, never the phone or the code. No authorization: the hook authenticates Kratos. |

Ports used by this context (wired in `internal/wiring`): `Recipients` (`Resolve` for kind/ref/language only; `PhoneFor` at SMS send time), `PushSender`, `SMSSender`, `SMSLinks` (fills `{link}` at send time: the emergency plan and SOS links of an external contact, minted by emergency, given the template, the recipient and the notification's params such as `sos_id`; optional).

## Providers

| Package | Implements | Notes |
|---|---|---|
| `adapter/push` | `PushSender`: `FCM` (HTTP v1, Android), `APNs` (token auth, HTTP/2, iOS), `Router` (by device platform) | standard library only; OAuth2 token from a self-signed RS256 service-account JWT, cached until 5 min before expiry; APNs ES256 provider token renewed every 40 min (Apple's 20–60 min window); both safe for concurrent use |
| `adapter/smsgw` | `SMSSender`: `HTTPSender` + `Provider` interface; `SMSC` (SMSC.kz `send.php`, `fmt=3`) | a second gateway is one file implementing `Provider` (`Name`, `Request`, `Parse`) |
| `internal/wiring` | `LogPushSender`, `LogSMSSender` | development only (`BIRGE_DELIVERY_MODE=dev`); refused at startup otherwise |

Provider errors wrap one of two classes from `port`, and the service maps them onto the existing timer retry:

| Provider answer | Class | Service does |
|---|---|---|
| FCM `UNREGISTERED`, `SENDER_ID_MISMATCH`, 404, 400 naming `message.token`; APNs 410, `Unregistered`, `BadDeviceToken`, `DeviceTokenNotForTopic` | `port.ErrTokenInvalid` | removes that device registration (`DeleteRejectedDevice`, only while it still holds the rejected token); no retry for it |
| other 4xx (bad payload); SMSC codes 1, 5, 6, 7, 8, 9; platform without a provider | `port.ErrPermanent` | delivery `failed`, timer completes (no retry) |
| 429, 5xx, transport errors, 401/403 (our credentials), token endpoint failures; SMSC codes 2, 3, 4 and unknown | plain error | delivery `failed`, handler returns the error, the platform timer retries with `timers.Backoff` (10 s doubling to 1 h) |

Push payload: `notification_id` and the neutral `Title` (from `Config.PushTitles` in the member's language) only; the same high priority for every class, so the provider cannot tell critical from normal.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `service.Config.CriticalSMSAfter` | 60s | Wait for acknowledgement before member critical SMS |
| `service.Config.SMSTexts` | required for SMS | `template → language →` neutral fixed body (no message content); unset `BIRGE_SMS_TEXTS` → DEMO preset. `{link}` is replaced at send time through `SMSLinks`, after the guard and right before the provider. Ordinary SMS: no link → skipped (`no_link`), a transient error → the timer retries. Critical SMS (crisis path): never skipped and never retried for the link; any failure, or a mint slower than 5 s, sends at once the text under `<template>.no_link` (logged `CriticalSMSNoLink`; a missing variant cuts the placeholder out and logs `SMSNoLinkTextMissing`). The link is never stored or logged |
| `service.Config.PushTitles` | `Birge` | `language →` neutral push title (`BIRGE_PUSH_TITLES`) |
| `BIRGE_PUSH_PROVIDER`, `BIRGE_SMS_PROVIDER` and credential files | `log` in dev only | see `docs/development.md` § Configuration |
| `guard.Config.AllowedPrefixes` | empty rejects all | E.164 prefix allow-list |
| `guard.Config.PerNumberLimit` / `PerNumberWindow` | 0 disabled / 1h | Per-number rate limit (fixed window) |
| `guard.Config.GlobalLimit` / `GlobalWindow` | 0 disabled / 1m | Rate limit over all numbers (fixed window) |
| `guard.Config.DailyBudget` | 0 unlimited | UTC-day send budget (critical SMS count but are never blocked) |
| `guard.Config.Store` / `Name` | private memory table / `sms` | Counter store (`platform/ratelimit`); the worker passes the shared PostgreSQL store with names `notify` and `signin`, so every worker replica counts against one limit and one budget |
| `Service.SignInGuard` | nil refuses | Guard for `SendSignInCode`: built from the same `guard.Config`, separate counters |
| see `docs/development.md` § Configuration | | all variables, defaults and which subcommand reads them |

## Invariants

- Consumers idempotent via `processed_events`; unique `(request_id, recipient_kind, recipient_ref)` (database + service).
- `recipient_kind=member`: inbox + push; critical SMS only after acknowledgement timeout. `external`: SMS only, no inbox/push; phone looked up via `PhoneFor` at send time outside any DB transaction, never stored (service + schema).
- `Resolve` returns kind, ref and language only — no identity-provider I/O — so a Kratos outage cannot lose `DeliveryRequested`.
- Push and SMS carry no message content (ADR-0013); SMS bodies from config only. Push carries the notification ID and a neutral title; template, params and class never reach a provider (adapters).
- Provider adapters never log, and their error texts hold the HTTP status and the provider's reason code only: no token, phone, text or credential (adapters; tests grep logs and errors).
- Sign-in codes (`SendSignInCode`) are never persisted: no event, notification, delivery or timer row. They pass `SignInGuard`, whose counters are separate from `Guard`, so sign-in traffic cannot block a critical or plan SMS by per-number limit or daily budget (service; `service/signin_test.go` runs without a store).
- `sms/guard` before every SMS; critical uses `AllowCritical` (no daily budget). Guard rejects for critical → `CriticalSMSBlocked` + delivery `failed` (not `skipped`).
- Guard counters are shared by every worker replica (`BIRGE_RATE_LIMIT_STORE=postgres`): N workers keep one per-number limit, one global limit and one daily budget per guard. The store keeps only a keyed hash of the number. A counter store error refuses an ordinary SMS (`guard.ErrUnavailable`, delivery `skipped`) and never a critical one (`SMSGuardStoreError` logged, sent) (guard; `sms/guard/guard_test.go`).
- Logs and events: IDs and event names only — never text, phones or push tokens (service + ports).
- Marking critical read acknowledges and cancels CriticalSMS (service).
- A critical SMS whose text has `{link}` is never skipped or delayed for the link (crisis path, AGENTS.md non-negotiable 1): a failed, missing or slow (> 5 s) link sends the `<template>.no_link` text at once (service; `service/links_test.go` `TestCriticalSMSFallsBackWithoutLink`, `TestCriticalMemberSMSUsesTextWithoutLink`).
- Account deletion (AGENTS.md non-negotiable 6): her devices, her inbox (`recipient_kind=member`, ref = her) and every notification whose `params` hold her ID (a plan she sent carries `owner_id`) are deleted with their deliveries; their pending push and SMS timers are cancelled in the same transaction. Nothing is tombstoned: a notification is a message to one recipient, no other record depends on it. One audit entry `notify.member_deleted` (actor system).

## Failure modes

- Database down: inbox/device HTTP and timer handlers fail; JetStream messages are nacked/retried by the consumer.
- Recipients unresolved: `HandleDeliveryRequested` returns `ErrRecipientUnresolved` (message retried); no partial notification for that event until resolve succeeds after `MarkProcessed` — resolve runs before the transaction.
- `PhoneFor` failure (identity provider down): SMS timer handler returns an error; delivery stays pending and retries with backoff — never dropped silently. Empty phone → delivery `skipped`.
- Push/SMS provider down, 429 or 5xx: delivery marked `failed`; the handler returns the error and the timer retries with backoff (at-least-once: a provider that accepted but whose reply was lost may get the message again; SMSC answers code 9 to such a repeat within a minute, which is treated as permanent so no duplicate SMS is sent).
- Push token rejected: that device registration is removed; the app registers a fresh token on its next start. Other devices of the member are still tried.
- Permanent provider rejection (bad number, forbidden text, bad payload): delivery `failed`, no retry.
- Wrong provider credentials: every send fails transiently and retries with backoff until an operator fixes the file and restarts the worker. A credential file that does not parse refuses startup.
- Guard / missing SMS text: non-critical → `skipped`; critical guard → `failed` + `CriticalSMSBlocked` log; missing text → `skipped`.
- Link port down or slow: an ordinary SMS with `{link}` stays pending and retries; a critical one goes out at once without the link (`CriticalSMSLinkFailed`, `CriticalSMSNoLink`).
- Broker down: outbox from producers lags; this context's consumer stops receiving until JetStream recovers.

## Tests

- Unit: `service/ladder_test.go`, `service/pagination_test.go`, `service/provider_errors_test.go`, `service/signin_test.go` (guard refusals, separate budget, no store, no code or phone in logs), `service/links_test.go` (`{link}` minted at send time, skip and retry for ordinary SMS, the no-link fallback for critical SMS, no link in logs), `sms/guard/guard_test.go`; service tests use the `notifytest` fakes — ladder, idempotency, guard, cursor, provider error mapping.
- Providers: `adapter/push` (httptest FCM with token endpoint and renewal; HTTP/2 TLS httptest APNs; error mapping; bad key files; service end to end with a log grep) and `adapter/smsgw` (httptest SMSC: form, error codes, service end to end with a log grep).
- Integration: `adapter/integration_test.go` — PostgreSQL + NATS consumer path with `testinfra`; `DeleteRejectedDevice` keeps a re-registered token.
- Fakes: `notifytest/` (in-memory store, fake push/SMS).
