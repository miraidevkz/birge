-- name: InsertVersion :one
INSERT INTO knowledge.versions (
    id, article_id, lang, status, title, body, source_url,
    author_id, needs_sync, idempotency_key, created_at, updated_at
)
VALUES (
    @id, @article_id, @lang, @status, @title, @body, @source_url,
    @author_id, @needs_sync, @idempotency_key, @created_at, @updated_at
)
ON CONFLICT (author_id, idempotency_key) DO NOTHING
RETURNING id, article_id, lang, status, title, body, source_url,
          author_id, reviewer_id, reviewed_at, expires_at, reject_reason,
          needs_sync, idempotency_key, created_at, updated_at;

-- name: GetVersionByIdempotencyKey :one
SELECT id, article_id, lang, status, title, body, source_url,
       author_id, reviewer_id, reviewed_at, expires_at, reject_reason,
       needs_sync, idempotency_key, created_at, updated_at
FROM knowledge.versions
WHERE author_id = @author_id
  AND idempotency_key = @idempotency_key;

-- name: GetVersion :one
SELECT id, article_id, lang, status, title, body, source_url,
       author_id, reviewer_id, reviewed_at, expires_at, reject_reason,
       needs_sync, idempotency_key, created_at, updated_at
FROM knowledge.versions
WHERE id = @id;

-- name: GetVersionForUpdate :one
SELECT id, article_id, lang, status, title, body, source_url,
       author_id, reviewer_id, reviewed_at, expires_at, reject_reason,
       needs_sync, idempotency_key, created_at, updated_at
FROM knowledge.versions
WHERE id = @id
FOR UPDATE;

-- name: GetVersionWithArticle :one
SELECT v.id, v.article_id, v.lang, v.status, v.title, v.body, v.source_url,
       v.author_id, v.reviewer_id, v.reviewed_at, v.expires_at, v.reject_reason,
       v.needs_sync, v.idempotency_key, v.created_at, v.updated_at,
       a.topic_id, a.kind
FROM knowledge.versions v
JOIN knowledge.articles a ON a.id = v.article_id
WHERE v.id = @id;

-- name: GetVersionWithArticleForUpdate :one
SELECT v.id, v.article_id, v.lang, v.status, v.title, v.body, v.source_url,
       v.author_id, v.reviewer_id, v.reviewed_at, v.expires_at, v.reject_reason,
       v.needs_sync, v.idempotency_key, v.created_at, v.updated_at,
       a.topic_id, a.kind
FROM knowledge.versions v
JOIN knowledge.articles a ON a.id = v.article_id
WHERE v.id = @id
FOR UPDATE OF v;

-- name: UpdateDraft :one
UPDATE knowledge.versions
SET title = @title,
    body = @body,
    source_url = @source_url,
    reject_reason = NULL,
    updated_at = @updated_at
WHERE id = @id
  AND status = 'draft'
  AND author_id = @author_id
RETURNING id, article_id, lang, status, title, body, source_url,
          author_id, reviewer_id, reviewed_at, expires_at, reject_reason,
          needs_sync, idempotency_key, created_at, updated_at;

-- name: SubmitVersion :one
UPDATE knowledge.versions
SET status = 'in_review',
    reject_reason = NULL,
    updated_at = @updated_at
WHERE id = @id
  AND status = 'draft'
  AND author_id = @author_id
RETURNING id, article_id, lang, status, title, body, source_url,
          author_id, reviewer_id, reviewed_at, expires_at, reject_reason,
          needs_sync, idempotency_key, created_at, updated_at;

-- name: RejectVersion :one
UPDATE knowledge.versions
SET status = 'draft',
    reject_reason = @reject_reason,
    updated_at = @updated_at
WHERE id = @id
  AND status = 'in_review'
RETURNING id, article_id, lang, status, title, body, source_url,
          author_id, reviewer_id, reviewed_at, expires_at, reject_reason,
          needs_sync, idempotency_key, created_at, updated_at;

-- name: PublishVersion :one
UPDATE knowledge.versions
SET status = 'published',
    reviewer_id = @reviewer_id,
    reviewed_at = @reviewed_at,
    expires_at = @expires_at,
    needs_sync = false,
    reject_reason = NULL,
    updated_at = @updated_at
WHERE id = @id
  AND status = 'in_review'
RETURNING id, article_id, lang, status, title, body, source_url,
          author_id, reviewer_id, reviewed_at, expires_at, reject_reason,
          needs_sync, idempotency_key, created_at, updated_at;

-- name: RetirePublished :one
UPDATE knowledge.versions
SET status = 'retired',
    reviewer_id = NULL,
    reviewed_at = NULL,
    expires_at = NULL,
    updated_at = @updated_at
WHERE article_id = @article_id
  AND lang = @lang
  AND status = 'published'
  AND id <> @except_id
RETURNING id, article_id, lang, status, title, body, source_url,
          author_id, reviewer_id, reviewed_at, expires_at, reject_reason,
          needs_sync, idempotency_key, created_at, updated_at;

-- name: MarkOtherLanguagesNeedsSync :exec
UPDATE knowledge.versions
SET needs_sync = true,
    updated_at = @updated_at
WHERE article_id = @article_id
  AND lang <> @lang
  AND status <> 'retired';

-- name: ListStaffVersions :many
SELECT v.id, v.article_id, v.lang, v.status, v.title, v.body, v.source_url,
       v.author_id, v.reviewer_id, v.reviewed_at, v.expires_at, v.reject_reason,
       v.needs_sync, v.idempotency_key, v.created_at, v.updated_at,
       a.topic_id, a.kind
FROM knowledge.versions v
JOIN knowledge.articles a ON a.id = v.article_id
WHERE (sqlc.narg('status')::text IS NULL OR v.status = sqlc.narg('status')::text)
  AND (
      sqlc.narg('cursor_updated_at')::timestamptz IS NULL
      OR (v.updated_at, v.id) < (sqlc.narg('cursor_updated_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY v.updated_at DESC, v.id DESC
LIMIT @page_limit;
