# notify

In-app inbox (source of truth), push and SMS delivery behind ports, and the
critical-notification escalation ladder.

## Owns

- Schema `notify`: `notifications`, `deliveries`, `devices`
- HTTP: `GET /me/inbox`, `POST /me/inbox/{id}/read`, `PUT|DELETE /me/devices/{id}`
- Consumer of `notify.DeliveryRequested` (JetStream durable `notify-delivery-requested`)
- Timer kinds (ADR-0020):
  - `notify.Push` — push send after commit (due now; dedup = notification id)
  - `notify.ExternalSMS` — immediate SMS for phone-only recipients (due now)
  - `notify.CriticalSMS` — member critical SMS fallback after
    `NOTIFY_CRITICAL_SMS_AFTER` (default 60s)

## Invariants

- Consumers are idempotent via `platform.processed_events` (consumer
  `notify.DeliveryRequested`); a duplicated event creates one notification per
  `(request_id, recipient_kind, recipient_ref)`.
- `recipient_kind=member` (`recipient_ref` = member id): inbox + push; critical
  SMS only after the acknowledgement timeout.
- `recipient_kind=external` (`recipient_ref` = trusted_contact id): no inbox and
  no push; SMS is scheduled immediately for any class. Phone is re-resolved at
  send time through the `Recipients` port; never stored.
- Push and SMS carry no message content (ADR-0013). SMS bodies are neutral fixed
  strings from config, keyed by template and language.
- `sms/guard` runs before every SMS: allow-listed E.164 prefixes, per-number and
  global rate limits, daily budget circuit breaker. For `class=critical` the
  daily budget does not block; other guard rejections log
  `event=CriticalSMSBlocked` and mark the delivery `failed` (not `skipped`).
- Delivery timers run through `adapter.TimerHandlers` (`timers.Handler`). Status
  checks make each successful run at most once; at-least-once edge: if the
  provider accepts a message but the delivery status update fails, a timer retry
  may contact the provider again.
- Logs and events carry IDs and event names only — never message text, phone
  numbers or push tokens.
- Marking a critical notification read also acknowledges it and cancels the SMS timer.

## Ports

| Port | Purpose |
|---|---|
| `Recipients` | Resolve `trusted_contact_id` / `staff_role` (wired later by emergency/safety) |
| `PushSender` | Deliver push (fake in tests; real provider later) |
| `SMSSender` | Deliver SMS (fake in tests; real provider later) |

Wiring in `cmd/birge` is out of scope for wave 1.
