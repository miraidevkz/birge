-- name: InsertMatch :one
INSERT INTO circle.matches (
    id, member_a, member_b, request_a_id, request_b_id, format,
    status, created_at
)
VALUES (
    @id, @member_a, @member_b, @request_a_id, @request_b_id, @format,
    'proposed', @created_at
)
RETURNING id, member_a, member_b, request_a_id, request_b_id, format,
          answer_a, answer_b, met_a, met_b, status, room_id, ask_met_at, created_at;

-- name: GetMatch :one
SELECT id, member_a, member_b, request_a_id, request_b_id, format,
       answer_a, answer_b, met_a, met_b, status, room_id, ask_met_at, created_at
FROM circle.matches
WHERE id = @id;

-- name: GetMatchForUpdate :one
SELECT id, member_a, member_b, request_a_id, request_b_id, format,
       answer_a, answer_b, met_a, met_b, status, room_id, ask_met_at, created_at
FROM circle.matches
WHERE id = @id
FOR UPDATE;

-- name: ListMatchesForMember :many
SELECT id, member_a, member_b, request_a_id, request_b_id, format,
       answer_a, answer_b, met_a, met_b, status, room_id, ask_met_at, created_at
FROM circle.matches
WHERE member_a = @member_id OR member_b = @member_id
ORDER BY created_at DESC, id DESC
LIMIT @page_limit;

-- name: PairMatchedSince :one
-- True when this canonical pair already has a match created at or after since
-- (30-day rematch guard).
SELECT EXISTS (
    SELECT 1 FROM circle.matches
    WHERE member_a = @member_a
      AND member_b = @member_b
      AND created_at >= @since
)::boolean AS matched;

-- name: UpdateMatchAnswers :one
UPDATE circle.matches
SET answer_a = @answer_a,
    answer_b = @answer_b,
    status = @status,
    ask_met_at = @ask_met_at
WHERE id = @id
RETURNING id, member_a, member_b, request_a_id, request_b_id, format,
          answer_a, answer_b, met_a, met_b, status, room_id, ask_met_at, created_at;

-- name: UpdateMatchMet :one
UPDATE circle.matches
SET met_a = @met_a,
    met_b = @met_b,
    status = @status
WHERE id = @id
RETURNING id, member_a, member_b, request_a_id, request_b_id, format,
          answer_a, answer_b, met_a, met_b, status, room_id, ask_met_at, created_at;

-- name: SetMatchRoomID :one
UPDATE circle.matches
SET room_id = @room_id
WHERE id = @id
  AND room_id IS NULL
RETURNING id, member_a, member_b, request_a_id, request_b_id, format,
          answer_a, answer_b, met_a, met_b, status, room_id, ask_met_at, created_at;

-- name: SetMatchAskMetAt :one
UPDATE circle.matches
SET ask_met_at = @ask_met_at
WHERE id = @id
  AND ask_met_at IS NULL
RETURNING id, member_a, member_b, request_a_id, request_b_id, format,
          answer_a, answer_b, met_a, met_b, status, room_id, ask_met_at, created_at;
