// Package wiring assembles the Birge backend from environment configuration:
// it is what cmd/birge runs for `birge api`, `birge worker`, `birge migrate`
// and `birge kb-reindex` (keygen lives in cmd/birge and needs no wiring). It
// owns no data and no business rules; it chooses the adapters behind every
// port and the order of the HTTP middleware.
//
// What it owns:
//
//   - Config and Load: every environment variable, validated at startup, with
//     all problems reported at once and no secret values in the messages.
//     Key material is read from files named by the environment (ADR-0009),
//     never from the environment itself. docs/development.md § Configuration
//     lists the variables.
//   - The API stack (NewAPIHandler): request log → /auth/ gateway → /v1 rate
//     limit → authentication (staffauth for /v1/staff/, the member session
//     everywhere else; chat WebSocket authenticates in its first frame) →
//     request validation → generated handlers (meta, membership, privacy,
//     notify, safety, wellbeing, emergency, assistant, knowledge, circle,
//     chat, sharing, directory) and GET /v1/chat/ws.
//   - The worker (RunWorker): outbox relay to JetStream, the notify
//     DeliveryRequested consumer, the emergency.SosShared district-push
//     consumer, chat.RoomOpenerConsumer, sharing/directory moderation.Decided
//     consumers, the knowledge retrieval indexer (when TEI+Qdrant are set)
//     and the timers worker with notify, privacy, safety.EscalationBackup,
//     wellbeing.Reminder, emergency.SosAutoClose, assistant.RetentionPurge,
//     knowledge review/expire, circle match/expire/ask-met, chat crisis/push/
//     retention and sharing ProcessPhoto/AutoClose (ADR-0007, ADR-0020),
//     stopped together on shutdown or on the first failure.
//   - Migrate: the migrations embedded in the binary (package birge/db),
//     applied with goose under a PostgreSQL session lock, then db/roles.sql
//     when asked (ADR-0006), then DEMO membership reference and knowledge
//     seed when those tables are empty (presets; never overwrites).
//   - kb-reindex: rebuilds the Qdrant collection from PublishedFragments
//     (ADR-0010); requires TEI, Qdrant and BIRGE_EMBEDDING_MODEL_ID.
//   - Adapters between contexts and platform: the membership Encryptor on the
//     platform envelope, safety/wellbeing/emergency/assistant/chat Sealer (same
//     envelope with per-column associated data), safety MemberDirectory
//     (membership + Kratos phone), emergency/circle/chat/sharing/directory
//     MemberInfo/Blocks (membership; first name only where shown), chat Mentors
//     (membership role mentor) and Crisis (safety Detect + CrisisPhones +
//     OpenEscalation source chat), CircleMet Acquaintances (circle.Met),
//     assistant consent/lexicon/profiles/retrieval/LLM, wellbeing/assistant
//     CrisisOpener → safety.OpenEscalation, sharing ObjectStore
//     (platform/objectstore; Disabled when BIRGE_S3_* unset), the notify
//     Recipients (membership, safety.ResolveStaffRole/ShiftPhone, emergency
//     TrustedContactTarget/ContactPhone), and the log-only push and SMS senders.
//   - EVENTS stream retention (BIRGE_EVENTS_MAX_AGE / BIRGE_EVENTS_MAX_BYTES)
//     applied in EnsureStream on worker start.
//   - Crisis lexicon and other configuration presets: when BIRGE_* variables
//     for lexicon, questionnaire, SMS texts, consent versions, crisis phones
//     or the assistant prompt are unset, the embedded DEMO/EXAMPLE values
//     from package presets are used and logged once at Warn (event=DemoPreset).
//     An empty or unloadable lexicon still refuses to start (ADR-0023). Keys
//     stay required. Missing LLM key or TEI/Qdrant logs Warn LLMDisabled /
//     RetrievalDisabled and continues (degraded assistant / empty retrieval).
//     Missing BIRGE_S3_* logs Warn ObjectStorageDisabled; photo uploads answer
//     503 storage_not_configured and everything else keeps working.
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
