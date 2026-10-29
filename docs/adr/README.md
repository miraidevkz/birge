# Architecture decision records

Each ADR records one decision that is costly to reverse: the context, what we chose, what else we considered and what it costs us ([ADR-0001](0001-record-decisions.md)). An accepted ADR is not rewritten. A changed decision gets a new ADR that supersedes the old one; small details that do not change the decision go into a dated addendum.

New ADR: copy [0000-template.md](0000-template.md) to the next free number, start with `Status: Proposed`, and change it to `Accepted` when the change that implements it is merged.

| ADR | Decision | Status | Date |
| --- | --- | --- | --- |
| [0001](0001-record-decisions.md) | Record architecture decisions | Accepted | 2026-10-27 |
| [0002](0002-modular-monolith.md) | Modular monolith by bounded contexts | Accepted | 2026-10-27 |
| [0003](0003-go-backend.md) | Go for the backend | Accepted | 2026-10-27 |
| [0004](0004-react-native-clients.md) | React Native for all clients | Accepted | 2026-10-27 |
| [0005](0005-contracts-first.md) | Contracts before code | Accepted | 2026-10-27 |
| [0006](0006-database-and-migrations.md) | PostgreSQL, declarative schema, safe migrations | Accepted | 2026-10-27 |
| [0007](0007-messaging-outbox-nats.md) | NATS JetStream with a transactional outbox | Accepted | 2026-10-27 |
| [0008](0008-identity.md) | Identity: Ory, OIDC-agnostic | Accepted | 2026-10-27 |
| [0009](0009-encryption.md) | Envelope encryption with per-user keys | Accepted | 2026-10-27 |
| [0010](0010-retrieval.md) | Retrieval: versioned embeddings | Accepted | 2026-10-27 |
| [0011](0011-kubernetes-first.md) | Kubernetes first, Compose as alternative | Accepted | 2026-10-27 |
| [0012](0012-foss-only-dependencies.md) | FOSS-only dependencies | Accepted | 2026-10-27 |
| [0013](0013-data-residency.md) | Personal data stays in the jurisdiction | Accepted | 2026-10-27 |
| [0014](0014-ui-stack.md) | UI stack, motion and typography | Accepted | 2026-10-27 |
| [0015](0015-object-storage.md) | S3-compatible object storage, portable between providers | Accepted | 2026-10-27 |
| [0016](0016-chat.md) | In-app chat | Accepted | 2026-10-27 |
| [0017](0017-ai-evaluation.md) | AI regression evaluation | Accepted | 2026-10-27 |
| [0018](0018-client-compatibility.md) | Compatibility with installed app versions | Accepted | 2026-10-27 |
| [0019](0019-user-safety-and-media-policy.md) | Member blocking, media policy and illegal-content incidents | Accepted | 2026-10-27 |
| [0020](0020-scheduler.md) | Durable timers on PostgreSQL | Accepted | 2026-10-27 |
| [0021](0021-observability.md) | Self-hosted observability without personal data | Accepted | 2026-10-27 |
| [0022](0022-knowledge-base-workflow.md) | Knowledge base editing and review | Accepted | 2026-10-27 |
| [0023](0023-crisis-path.md) | Crisis path and escalations | Accepted | 2026-10-27 |
| [0024](0024-analytics.md) | Pilot metrics and analytics | Accepted | 2026-10-27 |
| [0025](0025-warm-design-language.md) | Warm design language | Accepted | 2026-10-28 |
