-- name: InsertDelivery :one
INSERT INTO notify.deliveries (notification_id, channel, status, attempts, created_at, updated_at)
VALUES (@notification_id, @channel, @status, @attempts, @created_at, @updated_at)
ON CONFLICT (notification_id, channel) DO NOTHING
RETURNING id, notification_id, channel, status, attempts, provider_message_id, created_at, updated_at;

-- name: GetDelivery :one
SELECT id, notification_id, channel, status, attempts, provider_message_id, created_at, updated_at
FROM notify.deliveries
WHERE notification_id = @notification_id AND channel = @channel;

-- name: UpdateDeliveryStatus :exec
UPDATE notify.deliveries
SET
    status = @status,
    attempts = @attempts,
    provider_message_id = @provider_message_id,
    updated_at = @updated_at
WHERE id = @id;
