-- name: InsertCheckin :one
INSERT INTO wellbeing.checkins (
    id, member_id, questionnaire_version, answers, phq4, pss4, flag, idempotency_key, completed_at
) VALUES (
    @id, @member_id, @questionnaire_version, @answers, @phq4, @pss4, @flag, @idempotency_key, @completed_at
)
ON CONFLICT (member_id, idempotency_key) DO NOTHING
RETURNING id, member_id, questionnaire_version, answers, phq4, pss4, flag, idempotency_key, completed_at;

-- name: GetCheckinByIdempotencyKey :one
SELECT id, member_id, questionnaire_version, answers, phq4, pss4, flag, idempotency_key, completed_at
FROM wellbeing.checkins
WHERE member_id = @member_id AND idempotency_key = @idempotency_key;

-- name: ListCheckinHistory :many
SELECT id, member_id, questionnaire_version, answers, phq4, pss4, flag, idempotency_key, completed_at
FROM wellbeing.checkins
WHERE member_id = @member_id
  AND completed_at >= @since
ORDER BY completed_at ASC, id ASC
LIMIT @lim;

-- name: ListCohortCheckins :many
-- Scores of members whose first check-in falls in [first_from, first_to), up to until,
-- ordered by member and time. Only the service reads these rows, to reduce them to
-- count, mean and median (ADR-0024); per-member scores never leave wellbeing.
WITH firsts AS (
    SELECT member_id, min(completed_at) AS first_at
    FROM wellbeing.checkins
    GROUP BY member_id
)
SELECT c.member_id, c.pss4, c.completed_at
FROM wellbeing.checkins c
JOIN firsts f ON f.member_id = c.member_id
WHERE f.first_at >= @first_from::timestamptz
  AND f.first_at < @first_to::timestamptz
  AND c.completed_at < @until
ORDER BY c.member_id ASC, c.completed_at ASC, c.id ASC;
