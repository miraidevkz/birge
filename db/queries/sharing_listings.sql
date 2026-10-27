-- Account deletion: her listings with the contact requests on them, her
-- photo uploads and her contact requests on other listings. Object keys are
-- collected first; the objects are deleted by the sharing.DeleteObjects
-- timer after commit.

-- name: ListMemberListingsForDeletion :many
SELECT id, photo_object_key FROM sharing.listings
WHERE author_id = @member_id
ORDER BY id
FOR UPDATE;

-- name: ListMemberUploadsForDeletion :many
SELECT id, object_key, processed_object_key FROM sharing.photo_uploads
WHERE member_id = @member_id
ORDER BY id
FOR UPDATE;

-- name: DeleteMemberContactRequests :execrows
DELETE FROM sharing.contact_requests
WHERE requester_id = @member_id OR listing_id = ANY(@listing_ids::uuid[]);

-- name: DeleteMemberListings :execrows
DELETE FROM sharing.listings WHERE author_id = @member_id;

-- An upload of hers can be referenced only by her own listings, deleted above.
-- name: DeleteMemberUploads :execrows
DELETE FROM sharing.photo_uploads WHERE member_id = @member_id;

-- name: InsertListing :one
INSERT INTO sharing.listings (
    id, author_id, idempotency_key, type, category, title, description,
    district_id, photo_upload_id, photo_object_key, status, created_at
) VALUES (
    @id, @author_id, @idempotency_key, @type, @category, @title, @description,
    @district_id, @photo_upload_id, NULL, 'pending', @created_at
)
ON CONFLICT (author_id, idempotency_key) DO NOTHING
RETURNING id, author_id, idempotency_key, type, category, title, description,
          district_id, photo_upload_id, photo_object_key, status, reject_reason,
          approved_at, closes_at, closed_at, created_at;

-- name: GetListingByIdempotencyKey :one
SELECT id, author_id, idempotency_key, type, category, title, description,
       district_id, photo_upload_id, photo_object_key, status, reject_reason,
       approved_at, closes_at, closed_at, created_at
FROM sharing.listings
WHERE author_id = @author_id
  AND idempotency_key = @idempotency_key;

-- name: GetListing :one
SELECT id, author_id, idempotency_key, type, category, title, description,
       district_id, photo_upload_id, photo_object_key, status, reject_reason,
       approved_at, closes_at, closed_at, created_at
FROM sharing.listings
WHERE id = @id;

-- name: GetListingForUpdate :one
SELECT id, author_id, idempotency_key, type, category, title, description,
       district_id, photo_upload_id, photo_object_key, status, reject_reason,
       approved_at, closes_at, closed_at, created_at
FROM sharing.listings
WHERE id = @id
FOR UPDATE;

-- name: ListMyListings :many
SELECT id, author_id, idempotency_key, type, category, title, description,
       district_id, photo_upload_id, photo_object_key, status, reject_reason,
       approved_at, closes_at, closed_at, created_at
FROM sharing.listings
WHERE author_id = @author_id
ORDER BY created_at DESC, id DESC
LIMIT @page_limit;

-- name: ListActiveListings :many
-- Active feed, newest first. Blocked authors are filtered by the service.
SELECT id, author_id, idempotency_key, type, category, title, description,
       district_id, photo_upload_id, photo_object_key, status, reject_reason,
       approved_at, closes_at, closed_at, created_at
FROM sharing.listings
WHERE status = 'active'
  AND (sqlc.narg(filter_type)::text IS NULL OR type = sqlc.narg(filter_type))
  AND (sqlc.narg(filter_category)::text IS NULL OR category = sqlc.narg(filter_category))
  AND (sqlc.narg(filter_district)::uuid IS NULL OR district_id = sqlc.narg(filter_district))
  AND author_id <> @viewer_id
  AND (
    sqlc.narg(cursor_created_at)::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT @page_limit;

-- name: SetListingPhotoObjectKey :one
UPDATE sharing.listings
SET photo_object_key = @photo_object_key
WHERE id = @id
RETURNING id, author_id, idempotency_key, type, category, title, description,
          district_id, photo_upload_id, photo_object_key, status, reject_reason,
          approved_at, closes_at, closed_at, created_at;

-- name: ApproveListing :one
-- Records moderation approval. Activation (status=active) is a separate step
-- that waits for a processed photo when one was attached.
UPDATE sharing.listings
SET approved_at = @approved_at
WHERE id = @id
  AND status = 'pending'
RETURNING id, author_id, idempotency_key, type, category, title, description,
          district_id, photo_upload_id, photo_object_key, status, reject_reason,
          approved_at, closes_at, closed_at, created_at;

-- name: ActivateListing :one
UPDATE sharing.listings
SET status = 'active', closes_at = @closes_at
WHERE id = @id
  AND status = 'pending'
  AND approved_at IS NOT NULL
  AND (photo_upload_id IS NULL OR photo_object_key IS NOT NULL)
RETURNING id, author_id, idempotency_key, type, category, title, description,
          district_id, photo_upload_id, photo_object_key, status, reject_reason,
          approved_at, closes_at, closed_at, created_at;

-- name: RejectListing :one
UPDATE sharing.listings
SET status = 'rejected', reject_reason = @reject_reason, approved_at = NULL, closes_at = NULL
WHERE id = @id
  AND status = 'pending'
RETURNING id, author_id, idempotency_key, type, category, title, description,
          district_id, photo_upload_id, photo_object_key, status, reject_reason,
          approved_at, closes_at, closed_at, created_at;

-- name: CloseListing :one
UPDATE sharing.listings
SET status = 'closed', closed_at = @closed_at
WHERE id = @id
  AND status = 'active'
RETURNING id, author_id, idempotency_key, type, category, title, description,
          district_id, photo_upload_id, photo_object_key, status, reject_reason,
          approved_at, closes_at, closed_at, created_at;
