-- name: InsertOutboxEvent :one
INSERT INTO platform.outbox (event_type, event_version, payload, trace_parent, occurred_at)
VALUES (@event_type, @event_version, @payload, @trace_parent, @occurred_at)
RETURNING id;

-- name: ClaimUnpublishedEvents :many
SELECT id, event_type, event_version, payload, trace_parent, occurred_at
FROM platform.outbox
WHERE published_at IS NULL
ORDER BY occurred_at, id
LIMIT @batch_size
FOR UPDATE SKIP LOCKED;

-- name: MarkEventsPublished :exec
UPDATE platform.outbox
SET published_at = @published_at
WHERE id = ANY(@ids::uuid[]);

-- name: DeletePublishedEventsBefore :execrows
DELETE FROM platform.outbox
WHERE published_at IS NOT NULL AND published_at < @before;

-- name: OldestUnpublishedEvent :one
SELECT min(occurred_at)::timestamptz AS oldest
FROM platform.outbox
WHERE published_at IS NULL;

-- name: EventProcessed :one
SELECT EXISTS (
    SELECT 1 FROM platform.processed_events
    WHERE consumer = @consumer AND event_id = @event_id
) AS found;

-- name: MarkEventProcessed :execrows
INSERT INTO platform.processed_events (consumer, event_id)
VALUES (@consumer, @event_id)
ON CONFLICT DO NOTHING;

-- OutboxLag feeds the outbox lag gauge (telemetry.Sampler): the oldest
-- unpublished occurred_at, or @now when everything is published (age 0).
-- name: OutboxLag :one
SELECT COALESCE(min(occurred_at), sqlc.arg(now)::timestamptz)::timestamptz AS oldest
FROM platform.outbox
WHERE published_at IS NULL;

-- UntakenEscalations feeds the unattended-escalation alert
-- (telemetry.Sampler, ADR-0021): escalations opened since @since with no
-- EscalationTaken or EscalationClosed event yet. It reads only the outbox,
-- the platform's own table, and only escalation_id, the field the event
-- contracts (contracts/events/safety.Escalation*.v1.json) define for
-- every consumer; it never reads safety tables.
-- Outbox ids are uuidv7, ordered by insert time: @min_id is the smallest
-- uuidv7 of @since, so the primary key bounds the scan to recent rows
-- without an extra index.
-- name: UntakenEscalations :one
SELECT count(*)::bigint AS untaken,
       COALESCE(min(o.occurred_at), sqlc.arg(now)::timestamptz)::timestamptz AS oldest
FROM platform.outbox o
WHERE o.id >= sqlc.arg(min_id)::uuid
  AND o.event_type = 'safety.EscalationOpened'
  AND o.occurred_at >= sqlc.arg(since)::timestamptz
  AND NOT EXISTS (
    SELECT 1
    FROM platform.outbox d
    WHERE d.id >= sqlc.arg(min_id)::uuid
      AND d.event_type IN ('safety.EscalationTaken', 'safety.EscalationClosed')
      AND d.payload->>'escalation_id' = o.payload->>'escalation_id'
  );
