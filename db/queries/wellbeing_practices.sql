-- name: ListPracticesByLang :many
SELECT id, lang, title_key, duration_seconds, object_key, created_at
FROM wellbeing.practices
WHERE lang = @lang
ORDER BY created_at ASC, id ASC
LIMIT 50;

-- name: InsertPractice :one
INSERT INTO wellbeing.practices (lang, title_key, duration_seconds, object_key)
VALUES (@lang, @title_key, @duration_seconds, @object_key)
RETURNING id, lang, title_key, duration_seconds, object_key, created_at;
