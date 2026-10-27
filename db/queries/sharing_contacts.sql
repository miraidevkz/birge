-- name: InsertContactRequest :one
INSERT INTO sharing.contact_requests (id, listing_id, requester_id, status, created_at)
VALUES (@id, @listing_id, @requester_id, 'pending', @created_at)
ON CONFLICT (listing_id, requester_id) DO NOTHING
RETURNING id, listing_id, requester_id, status, created_at, accepted_at;

-- name: GetContactRequestByListingRequester :one
SELECT id, listing_id, requester_id, status, created_at, accepted_at
FROM sharing.contact_requests
WHERE listing_id = @listing_id
  AND requester_id = @requester_id;

-- name: GetContactRequest :one
SELECT id, listing_id, requester_id, status, created_at, accepted_at
FROM sharing.contact_requests
WHERE id = @id;

-- name: GetContactRequestForUpdate :one
SELECT id, listing_id, requester_id, status, created_at, accepted_at
FROM sharing.contact_requests
WHERE id = @id
FOR UPDATE;

-- name: AcceptContactRequest :one
UPDATE sharing.contact_requests
SET status = 'accepted', accepted_at = @accepted_at
WHERE id = @id
  AND status = 'pending'
RETURNING id, listing_id, requester_id, status, created_at, accepted_at;
