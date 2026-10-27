-- name: InsertPhotoUpload :one
INSERT INTO sharing.photo_uploads (
    id, member_id, object_key, content_type, size_bytes, expires_at, status, created_at
) VALUES (
    @id, @member_id, @object_key, @content_type, @size_bytes, @expires_at, 'pending', @created_at
)
RETURNING id, member_id, object_key, content_type, size_bytes, expires_at, status,
          processed_object_key, used_at, created_at;

-- name: GetPhotoUpload :one
SELECT id, member_id, object_key, content_type, size_bytes, expires_at, status,
       processed_object_key, used_at, created_at
FROM sharing.photo_uploads
WHERE id = @id;

-- name: GetPhotoUploadForUpdate :one
SELECT id, member_id, object_key, content_type, size_bytes, expires_at, status,
       processed_object_key, used_at, created_at
FROM sharing.photo_uploads
WHERE id = @id
FOR UPDATE;

-- name: MarkPhotoUploadUsed :one
UPDATE sharing.photo_uploads
SET used_at = @used_at
WHERE id = @id
  AND member_id = @member_id
  AND used_at IS NULL
  AND expires_at > @now
  AND status = 'pending'
RETURNING id, member_id, object_key, content_type, size_bytes, expires_at, status,
          processed_object_key, used_at, created_at;

-- name: MarkPhotoUploadProcessed :one
UPDATE sharing.photo_uploads
SET status = 'processed', processed_object_key = @processed_object_key
WHERE id = @id
  AND status = 'pending'
RETURNING id, member_id, object_key, content_type, size_bytes, expires_at, status,
          processed_object_key, used_at, created_at;

-- name: MarkPhotoUploadFailed :one
UPDATE sharing.photo_uploads
SET status = 'failed'
WHERE id = @id
  AND status = 'pending'
RETURNING id, member_id, object_key, content_type, size_bytes, expires_at, status,
          processed_object_key, used_at, created_at;
