-- name: UpsertReview :one
-- One review per (specialist, author): insert or replace and return to pending.
-- The row id is kept on conflict so in-flight moderation items stay valid.
INSERT INTO directory.reviews (
    id, specialist_id, author_id, rating, text, status, created_at, updated_at
) VALUES (
    @id, @specialist_id, @author_id, @rating, @text, 'pending', @created_at, @updated_at
)
ON CONFLICT (specialist_id, author_id) DO UPDATE
SET rating = EXCLUDED.rating,
    text = EXCLUDED.text,
    status = 'pending',
    reject_reason = NULL,
    updated_at = EXCLUDED.updated_at
RETURNING id, specialist_id, author_id, rating, text, status, reject_reason, created_at, updated_at;

-- name: GetReview :one
SELECT id, specialist_id, author_id, rating, text, status, reject_reason, created_at, updated_at
FROM directory.reviews
WHERE id = @id;

-- name: GetReviewForUpdate :one
SELECT id, specialist_id, author_id, rating, text, status, reject_reason, created_at, updated_at
FROM directory.reviews
WHERE id = @id
FOR UPDATE;

-- name: GetReviewBySpecialistAuthor :one
SELECT id, specialist_id, author_id, rating, text, status, reject_reason, created_at, updated_at
FROM directory.reviews
WHERE specialist_id = @specialist_id
  AND author_id = @author_id;

-- name: ListLatestActiveReviews :many
SELECT id, specialist_id, author_id, rating, text, status, reject_reason, created_at, updated_at
FROM directory.reviews
WHERE specialist_id = @specialist_id
  AND status = 'active'
ORDER BY created_at DESC, id DESC
LIMIT @page_limit;

-- name: AverageActiveRating :one
SELECT coalesce(avg(rating), 0)::float8 AS average,
       count(*)::integer AS review_count
FROM directory.reviews
WHERE specialist_id = @specialist_id
  AND status = 'active';

-- name: ApproveReview :one
UPDATE directory.reviews
SET status = 'active', reject_reason = NULL, updated_at = @updated_at
WHERE id = @id
  AND status = 'pending'
RETURNING id, specialist_id, author_id, rating, text, status, reject_reason, created_at, updated_at;

-- name: RejectReview :one
UPDATE directory.reviews
SET status = 'rejected', reject_reason = @reject_reason, updated_at = @updated_at
WHERE id = @id
  AND status = 'pending'
RETURNING id, specialist_id, author_id, rating, text, status, reject_reason, created_at, updated_at;
