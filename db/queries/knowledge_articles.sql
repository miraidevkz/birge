-- name: InsertArticle :one
INSERT INTO knowledge.articles (id, topic_id, kind, created_at)
VALUES (@id, @topic_id, @kind, @created_at)
RETURNING id, topic_id, kind, created_at;

-- name: GetArticle :one
SELECT id, topic_id, kind, created_at
FROM knowledge.articles
WHERE id = @id;

-- name: ListTopicArticleSummaries :many
-- Published versions of a topic in lang (member catalogue).
SELECT a.id, v.title, v.expires_at, a.kind
FROM knowledge.articles a
JOIN knowledge.versions v ON v.article_id = a.id
WHERE a.topic_id = @topic_id
  AND v.lang = @lang
  AND v.status = 'published'
ORDER BY v.title, a.id
LIMIT 200;

-- name: GetPublishedArticle :one
-- The published version of an article in lang, with topic and kind.
SELECT a.id AS article_id, a.topic_id, a.kind, v.id AS version_id, v.lang,
       v.title, v.body, v.source_url, v.reviewed_at, v.expires_at
FROM knowledge.articles a
JOIN knowledge.versions v ON v.article_id = a.id
WHERE a.id = @article_id
  AND v.lang = @lang
  AND v.status = 'published';

-- name: ListPublishedFragments :many
-- Current (non-expired) published versions of a kind in lang, for retrieval.
SELECT a.id AS article_id, v.id AS version_id, v.title, v.source_url, v.body
FROM knowledge.articles a
JOIN knowledge.versions v ON v.article_id = a.id
WHERE a.kind = @kind
  AND v.lang = @lang
  AND v.status = 'published'
  AND v.expires_at > @now
ORDER BY v.title, a.id;
