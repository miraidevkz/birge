-- Reads for members and the API return active rows only: a deactivated entry
-- is hidden from new choices, while members who chose it keep its ID.

-- name: ListCities :many
SELECT id, code
FROM membership.cities
WHERE active
ORDER BY code;

-- name: ListDistricts :many
SELECT d.id, d.city_id, d.code
FROM membership.districts d
JOIN membership.cities c ON c.id = d.city_id
WHERE d.active AND c.active
ORDER BY d.city_id, d.code;

-- name: DistrictSelectable :one
SELECT EXISTS (
    SELECT 1
    FROM membership.districts d
    JOIN membership.cities c ON c.id = d.city_id
    WHERE d.id = @id AND d.active AND c.active
) AS found;

-- name: CountSelectableNeeds :one
SELECT count(*) FROM membership.needs
WHERE code = ANY(@codes::text[]) AND active;

-- name: ListNeeds :many
SELECT code
FROM membership.needs
WHERE active
ORDER BY code;

-- Reference sync (birge migrate). The lock serializes concurrent migrate
-- jobs so their reads and inserts do not interleave.

-- name: LockReference :exec
SELECT pg_advisory_xact_lock(hashtext('membership.reference'));

-- name: ListAllCities :many
SELECT id, code, active
FROM membership.cities
ORDER BY code;

-- name: ListAllDistricts :many
SELECT id, city_id, code, active
FROM membership.districts
ORDER BY city_id, code;

-- name: ListAllNeeds :many
SELECT code, active
FROM membership.needs
ORDER BY code;

-- name: InsertCity :exec
INSERT INTO membership.cities (id, code, created_at, updated_at)
VALUES (@id, @code, @now, @now);

-- name: InsertDistrict :exec
INSERT INTO membership.districts (id, city_id, code, created_at, updated_at)
VALUES (@id, @city_id, @code, @now, @now);

-- name: InsertNeed :exec
INSERT INTO membership.needs (code, created_at, updated_at)
VALUES (@code, @now, @now);

-- name: SetCityActive :exec
UPDATE membership.cities
SET active = @active, updated_at = @now
WHERE id = @id;

-- name: SetDistrictActive :exec
UPDATE membership.districts
SET active = @active, updated_at = @now
WHERE id = @id;

-- name: SetNeedActive :exec
UPDATE membership.needs
SET active = @active, updated_at = @now
WHERE code = @code;
