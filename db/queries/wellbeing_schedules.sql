-- name: UpsertSchedule :one
INSERT INTO wellbeing.schedules (member_id, weekday, hour, enabled, updated_at)
VALUES (@member_id, @weekday, @hour, @enabled, @updated_at)
ON CONFLICT (member_id) DO UPDATE
SET weekday = EXCLUDED.weekday,
    hour = EXCLUDED.hour,
    enabled = EXCLUDED.enabled,
    updated_at = EXCLUDED.updated_at
RETURNING member_id, weekday, hour, enabled, updated_at;

-- Account deletion.
-- name: DeleteMemberSchedule :execrows
DELETE FROM wellbeing.schedules WHERE member_id = @member_id;

-- name: DeleteMemberCheckins :execrows
DELETE FROM wellbeing.checkins WHERE member_id = @member_id;

-- name: GetSchedule :one
SELECT member_id, weekday, hour, enabled, updated_at
FROM wellbeing.schedules
WHERE member_id = @member_id;
