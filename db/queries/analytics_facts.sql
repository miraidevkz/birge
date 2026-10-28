-- name: LockPseudonym :exec
-- Serializes storing a fact with erasing the member it belongs to (ADR-0024).
-- Under READ COMMITTED an insert could check erased_actors before an
-- erasure commits while the erasure's DELETE cannot see the uncommitted
-- insert; both would commit and leave a fact of a deleted member. Both
-- sides take this transaction lock first: a fact with an actor locks the
-- actor, a fact without one locks its subject, and EraseActor locks her
-- actor and every subject it erases. The key is (class, first 4 bytes of
-- the pseudonym): the pseudonyms are HMAC output, so 4 bytes spread evenly;
-- a collision only makes two unrelated transactions wait for each other.
-- The class keeps actors and subjects apart, and the two-key form does not
-- overlap the one-bigint locks other contexts take.
SELECT pg_advisory_xact_lock(
    hashtext(@class::text),
    ('x' || encode(substring(@pseudonym::bytea FROM 1 FOR 4), 'hex'))::bit(32)::integer);

-- name: InsertFact :exec
-- A fact of an erased member is dropped, so her late events stay uncounted:
-- one with an actor when the actor is erased, one without an actor when its
-- subject (her SOS, escalation or answer) is erased. Runs after LockPseudonym.
INSERT INTO analytics.facts (id, event_type, occurred_at, code, actor, subject)
SELECT @id::uuid, @event_type::text, @occurred_at::timestamptz, sqlc.narg(code)::text, sqlc.narg(actor)::bytea, sqlc.narg(subject)::bytea
WHERE CASE
    WHEN sqlc.narg(actor)::bytea IS NOT NULL THEN
        NOT EXISTS (SELECT 1 FROM analytics.erased_actors e WHERE e.actor = sqlc.narg(actor)::bytea)
    WHEN sqlc.narg(subject)::bytea IS NOT NULL THEN
        NOT EXISTS (SELECT 1 FROM analytics.erased_subjects e WHERE e.subject = sqlc.narg(subject)::bytea)
    ELSE true
END;

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

-- name: RightsOutcomes :one
-- Rights answers (QuestionAnswered with scope rights) given in
-- [from_at, to_at), by members (the answer's actor):
--   - answered_from_knowledge: those with source knowledge;
--   - rated / helped: those that have a rating (at any time), judged by
--     their latest rating, since a member may change it and the last one wins.
-- A ReplyRated fact names no member; it is joined to its answer by the
-- message subject here, at report time, so the order in which the two events
-- arrive does not matter. A rating whose answer is not stored (not arrived
-- yet, or erased with its member) joins nothing and is not counted.
WITH answered AS (
    SELECT q.subject, q.actor, q.code
    FROM analytics.facts q
    WHERE q.event_type = 'assistant.QuestionAnswered'
      AND q.code LIKE 'rights\_%'
      AND q.subject IS NOT NULL AND q.actor IS NOT NULL
      AND q.occurred_at >= @from_at::timestamptz AND q.occurred_at < @to_at::timestamptz
), rated AS (
    SELECT DISTINCT ON (a.subject) a.subject, a.actor, r.code AS rating
    FROM answered a
    JOIN analytics.facts r ON r.subject = a.subject AND r.event_type = 'assistant.ReplyRated'
    ORDER BY a.subject, r.occurred_at DESC, r.id DESC
)
SELECT
    (SELECT count(*) FROM answered WHERE code = 'rights_knowledge')::integer                AS answered_from_knowledge,
    (SELECT count(DISTINCT actor) FROM answered WHERE code = 'rights_knowledge')::integer   AS knowledge_members,
    (SELECT count(*) FROM rated)::integer                                                   AS rated,
    (SELECT count(*) FROM rated WHERE rating = 'helped')::integer                           AS helped,
    (SELECT count(DISTINCT actor) FROM rated)::integer                                      AS rated_members;
