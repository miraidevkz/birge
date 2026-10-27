// Package wiring assembles the Birge backend from environment configuration:
// it is what cmd/birge runs for `birge api`, `birge worker` and
// `birge migrate` (keygen lives in cmd/birge and needs no wiring). It owns
// no data and no business rules; it chooses the adapters behind every port
// and the order of the HTTP middleware.
//
// What it owns:
//
//   - Config and Load: every environment variable, validated at startup, with
//     all problems reported at once and no secret values in the messages.
//     Key material is read from files named by the environment (ADR-0009),
//     never from the environment itself. docs/development.md § Configuration
//     lists the variables.
//   - The API stack (NewAPIHandler): request log → /auth/ gateway → /v1 rate
//     limit → session → request validation hook → generated handlers.
//   - The worker (RunWorker): outbox relay to JetStream, the notify
//     DeliveryRequested consumer and the timers worker (ADR-0007, ADR-0020),
//     stopped together on shutdown or on the first failure.
//   - Migrate: the migrations embedded in the binary (package birge/db),
//     applied with goose under a PostgreSQL session lock, then db/roles.sql
//     when asked (ADR-0006).
//   - Adapters between contexts and platform: the membership Encryptor on the
//     platform envelope, the notify Recipients (membership for Resolve;
//     Kratos admin only in PhoneFor at SMS send time), and the log-only
//     push and SMS senders.
//   - EVENTS stream retention (BIRGE_EVENTS_MAX_AGE / BIRGE_EVENTS_MAX_BYTES)
//     applied in EnsureStream on worker start.
//
// Invariants:
//
//   - Push and SMS are never dropped silently: the worker starts only with a
//     real provider, or with BIRGE_DELIVERY_MODE=dev, where the log-only
//     senders log the notification ID and channel and nothing else.
//   - Logs carry IDs, route patterns and event names; never phone numbers,
//     tokens, bodies, query strings or client IPs (ADR-0021).
//   - The client IP for rate limits comes from RemoteAddr unless a trusted
//     proxy header is configured, and then only from its right-most value.
//   - Ciphertexts are bound to their column and row through associated data
//     (crypto.AssociatedData); DisplayNameField is part of the schema.
package wiring
