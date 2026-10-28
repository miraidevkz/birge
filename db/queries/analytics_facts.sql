-- name: InsertFact :exec
-- A fact for an erased pseudonym is dropped: her late events stay uncounted.
INSERT INTO analytics.facts (id, event_type, occurred_at, code, actor, subject)
SELECT @id::uuid, @event_type::text, @occurred_at::timestamptz, sqlc.narg(code)::text, sqlc.narg(actor)::bytea, sqlc.narg(subject)::bytea
WHERE sqlc.narg(actor)::bytea IS NULL
   OR NOT EXISTS (SELECT 1 FROM analytics.erased_actors e WHERE e.actor = sqlc.narg(actor)::bytea);

-- name: CountFacts :many
-- Per event type in [from_at, to_at): facts, distinct subjects, distinct actors.
SELECT event_type,
       count(*)::integer                 AS facts,
       count(DISTINCT subject)::integer  AS subjects,
       count(DISTINCT actor)::integer    AS members
FROM analytics.facts
WHERE occurred_at >= @from_at AND occurred_at < @to_at
  AND event_type = ANY(@event_types::text[])
GROUP BY event_type;

-- name: ActiveMembersByWeek :many
-- Distinct active members per week (Monday, deployment time zone).
SELECT date_trunc('week', occurred_at AT TIME ZONE @tz::text)::date AS bucket,
       count(DISTINCT actor)::integer                               AS members
FROM analytics.facts
WHERE event_type = 'membership.MemberActiveDay'
  AND actor IS NOT NULL
  AND occurred_at >= @from_at AND occurred_at < @to_at
GROUP BY 1
ORDER BY 1;

-- name: ActiveMembersByMonth :many
-- Distinct active members per calendar month (deployment time zone).
SELECT date_trunc('month', occurred_at AT TIME ZONE @tz::text)::date AS bucket,
       count(DISTINCT actor)::integer                                AS members
FROM analytics.facts
WHERE event_type = 'membership.MemberActiveDay'
  AND actor IS NOT NULL
  AND occurred_at >= @from_at AND occurred_at < @to_at
GROUP BY 1
ORDER BY 1;

-- name: Retention :one
-- Cohort: members whose first active day is in [from_at, to_at). A member is
-- eligible for week N once her window [first + N weeks, first + N weeks + 7
-- days) has ended by now_at, and retained when she was active in it.
WITH firsts AS (
    SELECT a.actor, min(a.occurred_at) AS first_at
    FROM analytics.facts a
    WHERE a.event_type = 'membership.MemberActiveDay' AND a.actor IS NOT NULL
    GROUP BY a.actor
), cohort AS (
    SELECT fs.actor, fs.first_at,
           fs.first_at + interval '35 days' <= @now_at::timestamptz AS eligible4w,
           fs.first_at + interval '63 days' <= @now_at::timestamptz AS eligible8w,
           EXISTS (
               SELECT 1 FROM analytics.facts w
               WHERE w.event_type = 'membership.MemberActiveDay' AND w.actor = fs.actor
                 AND w.occurred_at >= fs.first_at + interval '28 days'
                 AND w.occurred_at < fs.first_at + interval '35 days') AS active4w,
           EXISTS (
               SELECT 1 FROM analytics.facts w
               WHERE w.event_type = 'membership.MemberActiveDay' AND w.actor = fs.actor
                 AND w.occurred_at >= fs.first_at + interval '56 days'
                 AND w.occurred_at < fs.first_at + interval '63 days') AS active8w
    FROM firsts fs
    WHERE fs.first_at >= @from_at::timestamptz AND fs.first_at < @to_at::timestamptz
)
SELECT
    count(*) FILTER (WHERE eligible4w)::integer              AS eligible4w,
    count(*) FILTER (WHERE eligible4w AND active4w)::integer AS retained4w,
    count(*) FILTER (WHERE eligible8w)::integer              AS eligible8w,
    count(*) FILTER (WHERE eligible8w AND active8w)::integer AS retained8w
FROM cohort;

-- name: SosOutcomes :one
-- SOS raised in [from_at, to_at): how many, by how many members, how many
-- were closed (at any time), and the first accepted response of each.
WITH raised AS (
    SELECT s.subject, s.actor, s.occurred_at
    FROM analytics.facts s
    WHERE s.event_type = 'emergency.SosRaised' AND s.subject IS NOT NULL
      AND s.occurred_at >= @from_at::timestamptz AND s.occurred_at < @to_at::timestamptz
), responded AS (
    SELECT r.subject, r.actor, extract(epoch FROM min(f.occurred_at) - r.occurred_at) / 60 AS minutes
    FROM raised r
    JOIN analytics.facts f ON f.subject = r.subject AND f.event_type = 'emergency.SosResponseAccepted'
    GROUP BY r.subject, r.actor, r.occurred_at
)
SELECT
    (SELECT count(*) FROM raised)::integer                AS raised,
    (SELECT count(DISTINCT actor) FROM raised)::integer   AS raised_members,
    (SELECT count(*) FROM raised r WHERE EXISTS (
        SELECT 1 FROM analytics.facts f
        WHERE f.subject = r.subject AND f.event_type = 'emergency.SosClosed'))::integer AS closed,
    (SELECT count(*) FROM responded)::integer              AS responded,
    (SELECT count(DISTINCT actor) FROM responded)::integer AS responded_members,
    (SELECT coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY minutes), 0) FROM responded)::float8 AS median_minutes;

-- name: EscalationOutcomes :one
-- Escalations opened in [from_at, to_at): how many, by how many members, and
-- how many were taken within within_seconds of opening.
WITH opened AS (
    SELECT e.subject, e.actor, e.occurred_at
    FROM analytics.facts e
    WHERE e.event_type = 'safety.EscalationOpened' AND e.subject IS NOT NULL
      AND e.occurred_at >= @from_at::timestamptz AND e.occurred_at < @to_at::timestamptz
)
SELECT
    count(*)::integer              AS opened,
    count(DISTINCT o.actor)::integer AS members,
    count(*) FILTER (WHERE EXISTS (
        SELECT 1 FROM analytics.facts f
        WHERE f.subject = o.subject AND f.event_type = 'safety.EscalationTaken'
          AND extract(epoch FROM f.occurred_at - o.occurred_at) <= @within_seconds::float8))::integer AS taken_within
FROM opened o;
