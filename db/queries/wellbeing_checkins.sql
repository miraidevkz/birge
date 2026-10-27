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
