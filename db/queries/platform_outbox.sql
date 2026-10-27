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
