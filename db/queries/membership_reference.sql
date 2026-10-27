-- name: ListCities :many
SELECT id, code
FROM membership.cities
ORDER BY code;

-- name: ListDistricts :many
SELECT id, city_id, code
FROM membership.districts
ORDER BY city_id, code;

-- name: DistrictExists :one
SELECT EXISTS (SELECT 1 FROM membership.districts WHERE id = @id) AS found;

-- name: CountKnownNeeds :one
SELECT count(*) FROM membership.needs
WHERE code = ANY(@codes::text[]);

-- name: ListNeeds :many
SELECT code
FROM membership.needs
ORDER BY code;

-- name: ReferenceIsEmpty :one
SELECT NOT EXISTS (
    SELECT 1 FROM membership.cities
    UNION ALL
    SELECT 1 FROM membership.districts
    UNION ALL
    SELECT 1 FROM membership.needs
) AS empty;

-- name: InsertCity :one
INSERT INTO membership.cities (code)
VALUES (@code)
RETURNING id;

-- name: InsertDistrict :exec
INSERT INTO membership.districts (city_id, code)
VALUES (@city_id, @code);

-- name: InsertNeed :exec
INSERT INTO membership.needs (code)
VALUES (@code);
