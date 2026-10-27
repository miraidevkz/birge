-- name: InsertTopic :one
INSERT INTO knowledge.topics (id, kind, code, title_kk, title_ru, title_en)
VALUES (@id, @kind, @code, @title_kk, @title_ru, @title_en)
RETURNING id, kind, code, title_kk, title_ru, title_en, created_at;

-- name: GetTopic :one
SELECT id, kind, code, title_kk, title_ru, title_en, created_at
FROM knowledge.topics
WHERE id = @id;

-- name: ListAllTopics :many
SELECT id, kind, code, title_kk, title_ru, title_en, created_at
FROM knowledge.topics
ORDER BY kind, code
LIMIT 200;

-- name: ListMemberTopics :many
-- Topics of the given kind that have at least one published version in lang.
SELECT DISTINCT t.id, t.kind, t.code, t.title_kk, t.title_ru, t.title_en, t.created_at
FROM knowledge.topics t
JOIN knowledge.articles a ON a.topic_id = t.id
JOIN knowledge.versions v ON v.article_id = a.id
WHERE t.kind = @kind
  AND v.lang = @lang
  AND v.status = 'published'
ORDER BY t.code
LIMIT 100;

-- name: KnowledgeIsEmpty :one
SELECT NOT EXISTS (
    SELECT 1 FROM knowledge.topics
    UNION ALL
    SELECT 1 FROM knowledge.articles
    UNION ALL
    SELECT 1 FROM knowledge.versions
) AS empty;

-- name: KnowledgeSchemaExists :one
SELECT EXISTS (
    SELECT 1 FROM information_schema.schemata WHERE schema_name = 'knowledge'
) AS present;
