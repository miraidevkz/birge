-- name: UpsertDevice :exec
INSERT INTO notify.devices (member_id, device_id, platform, push_token, updated_at)
VALUES (@member_id, @device_id, @platform, @push_token, @updated_at)
ON CONFLICT (member_id, device_id) DO UPDATE
SET platform = EXCLUDED.platform,
    push_token = EXCLUDED.push_token,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteDevice :execrows
DELETE FROM notify.devices
WHERE member_id = @member_id AND device_id = @device_id;

-- name: ListDevices :many
SELECT member_id, device_id, platform, push_token, updated_at
FROM notify.devices
WHERE member_id = @member_id;
