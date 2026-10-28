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

-- name: DeleteErasedSubjectsBefore :execrows
DELETE FROM analytics.erased_subjects WHERE erased_at < @before;

-- name: LockErasedSubjects :exec
-- Account deletion, step 0 (after locking her actor): lock every subject
-- MarkSubjectsErased will record, in a fixed order, so a fact without an
-- actor about one of them is either committed before the deletes below
-- (and deleted) or checked after erased_subjects has it (and dropped).
-- Same key as LockPseudonym with the subject class.
SELECT pg_advisory_xact_lock(hashtext(@class::text), k.key)
FROM (
    SELECT DISTINCT ('x' || encode(substring(h.subject FROM 1 FOR 4), 'hex'))::bit(32)::integer AS key
    FROM analytics.facts h
    WHERE h.actor = @actor AND h.subject IS NOT NULL
      AND h.event_type IN ('emergency.SosRaised', 'safety.EscalationOpened', 'assistant.QuestionAnswered')
    ORDER BY 1
) k;

-- name: MarkSubjectsErased :exec
-- Account deletion: record the subjects of her SOS, escalations and answers,
-- so a closing, taking or rating that arrives later is not stored. Runs
-- before DeleteActorFacts, which removes the facts it reads them from.
INSERT INTO analytics.erased_subjects (subject, erased_at)
SELECT DISTINCT h.subject, @erased_at::timestamptz
FROM analytics.facts h
WHERE h.actor = @actor AND h.subject IS NOT NULL
  AND h.event_type IN ('emergency.SosRaised', 'safety.EscalationOpened', 'assistant.QuestionAnswered')
ON CONFLICT (subject) DO NOTHING;

-- name: DeleteDependentFacts :execrows
-- Account deletion, step 1: the facts about her SOS, escalations and
-- assistant answers that carry no actor (closed, taken, rated), found
-- through her facts' subjects. Runs before DeleteActorFacts, which removes
-- the facts it looks them up by.
DELETE FROM analytics.facts f
WHERE f.actor IS NULL
  AND f.event_type IN ('emergency.SosClosed', 'safety.EscalationTaken', 'assistant.ReplyRated')
  AND f.subject IN (
      SELECT h.subject FROM analytics.facts h
      WHERE h.actor = @actor AND h.subject IS NOT NULL
        AND h.event_type IN ('emergency.SosRaised', 'safety.EscalationOpened', 'assistant.QuestionAnswered'));

-- name: DeleteActorFacts :execrows
-- Account deletion, step 2: every fact counted for her.
DELETE FROM analytics.facts WHERE actor = @actor;

-- name: MarkActorErased :exec
INSERT INTO analytics.erased_actors (actor, erased_at)
VALUES (@actor, @erased_at)
ON CONFLICT (actor) DO NOTHING;
