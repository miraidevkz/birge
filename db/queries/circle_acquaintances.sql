-- name: InsertAcquaintance :one
-- Idempotent: a repeated insert of the same pair returns the existing row.
INSERT INTO circle.acquaintances (member_a, member_b, since)
VALUES (@member_a, @member_b, @since)
ON CONFLICT (member_a, member_b) DO NOTHING
RETURNING member_a, member_b, since;

-- name: GetAcquaintance :one
SELECT member_a, member_b, since
FROM circle.acquaintances
WHERE member_a = @member_a
  AND member_b = @member_b;

-- name: ListAcquaintancesForMember :many
SELECT member_a, member_b, since
FROM circle.acquaintances
WHERE member_a = @member_id OR member_b = @member_id
ORDER BY since DESC;
