-- name: RollUpWeeks :execrows
-- Weekly counts of the facts older than before (Monday 00:00 in tz, so whole
-- weeks only), kept without actors. A week rolled once is never rolled again:
-- its facts are deleted in the same transaction.
INSERT INTO analytics.weekly_rollups (week, event_type, code, facts, members)
SELECT date_trunc('week', occurred_at AT TIME ZONE @tz::text)::date,
       event_type,
       coalesce(code, ''),
       count(*)::integer,
       count(DISTINCT actor)::integer
FROM analytics.facts
WHERE occurred_at < @before
GROUP BY 1, 2, 3
ON CONFLICT (week, event_type, code) DO NOTHING;

-- name: DeleteFactsBefore :execrows
DELETE FROM analytics.facts WHERE occurred_at < @before;

-- name: DeleteErasedBefore :execrows
DELETE FROM analytics.erased_actors WHERE erased_at < @before;

-- name: DeleteDependentFacts :execrows
-- Account deletion, step 1: the facts about her SOS and escalations that
-- carry no actor (closed, taken), found through her facts' subjects. Runs
-- before DeleteActorFacts, which removes the facts it looks them up by.
DELETE FROM analytics.facts f
WHERE f.actor IS NULL
  AND f.event_type IN ('emergency.SosClosed', 'safety.EscalationTaken')
  AND f.subject IN (
      SELECT h.subject FROM analytics.facts h
      WHERE h.actor = @actor AND h.subject IS NOT NULL
        AND h.event_type IN ('emergency.SosRaised', 'safety.EscalationOpened'));

-- name: DeleteActorFacts :execrows
-- Account deletion, step 2: every fact counted for her.
DELETE FROM analytics.facts WHERE actor = @actor;

-- name: MarkActorErased :exec
INSERT INTO analytics.erased_actors (actor, erased_at)
VALUES (@actor, @erased_at)
ON CONFLICT (actor) DO NOTHING;
