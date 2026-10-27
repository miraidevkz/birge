-- name: AppendConsent :one
INSERT INTO privacy.consent_history (member_id, type, version, granted, changed_at)
VALUES (@member_id, @type, @version, @granted, @changed_at)
RETURNING id, member_id, type, version, granted, changed_at;

-- name: GetLatestConsent :one
SELECT id, member_id, type, version, granted, changed_at
FROM privacy.consent_history
WHERE member_id = @member_id
  AND type = @type
ORDER BY changed_at DESC, id DESC
LIMIT 1;

-- name: ListLatestConsents :many
SELECT DISTINCT ON (type)
    id, member_id, type, version, granted, changed_at
FROM privacy.consent_history
WHERE member_id = @member_id
ORDER BY type, changed_at DESC, id DESC;
