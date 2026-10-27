# ADR-0007: NATS JetStream with a transactional outbox

Status: Accepted · Date: 2026-10-27

## Context
Crisis escalations, SOS and notifications must not be lost when a service crashes between the database write and the publish.

## Decision
Events are written to an `outbox` table in the same transaction as the data. A relay publishes them to NATS JetStream (file storage, acknowledgements, `Nats-Msg-Id` deduplication). Consumers are idempotent; delivery is at-least-once. Dead letters: `max_deliver` plus advisory handling.

## Alternatives
RabbitMQ: richer routing we do not need, heavier. Redis-based queues: not durable enough for critical paths.

## Consequences
Single-node JetStream durability equals disk durability plus backups; three replicas when the cluster grows.
