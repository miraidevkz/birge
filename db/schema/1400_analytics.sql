-- Desired schema of the analytics bounded context (declarative). See ADR-0024,
-- docs/requirements/mvp.md sections 9 and 10.
-- Analytics stores one fact per consumed domain event and nothing else: no
-- payload, no text, no district. People appear only as pseudonyms
-- (HMAC-SHA256 with the analytics key, BIRGE_ANALYTICS_KEY_FILE), which this
-- context can compute for a member it is told to delete but never reverse.

CREATE SCHEMA IF NOT EXISTS analytics;

-- One fact per consumed event (two for an event about a pair of members).
-- actor is the pseudonym of the member the fact counts for; NULL when the
-- event names no member (it is then counted through its subject).
-- subject is the pseudonym of the object the event is about (SOS,
-- escalation, match, Tea request, assistant message), so later events of the
-- same object can be joined without storing the object's ID.
-- code is a short enum value from the payload (SOS type, escalation source,
-- Tea format, rating), never prose.
-- Kept 24 months (analytics.Retention timer), then rolled into weekly_rollups.
CREATE TABLE analytics.facts (
    id          uuid        PRIMARY KEY,
    event_type  text        NOT NULL CHECK (event_type ~ '^[a-z]+\.[A-Za-z]{1,40}$'),
    occurred_at timestamptz NOT NULL,
    code        text        CHECK (code IS NULL OR code ~ '^[a-z][a-z_]{0,31}$'),
    actor       bytea       CHECK (actor IS NULL OR octet_length(actor) = 32),
    subject     bytea       CHECK (subject IS NULL OR octet_length(subject) = 32)
);

CREATE INDEX facts_type_time_idx ON analytics.facts (event_type, occurred_at);
CREATE INDEX facts_actor_idx ON analytics.facts (actor) WHERE actor IS NOT NULL;
CREATE INDEX facts_subject_idx ON analytics.facts (subject, event_type) WHERE subject IS NOT NULL;

-- Pseudonyms of deleted members. Events are consumed asynchronously, so an
-- event of hers may arrive after her deletion; a fact for an erased
-- pseudonym is never stored. Purged by the retention timer after a year
-- (longer than the JetStream stream may keep an event, BIRGE_EVENTS_MAX_AGE).
CREATE TABLE analytics.erased_actors (
    actor     bytea       PRIMARY KEY CHECK (octet_length(actor) = 32),
    erased_at timestamptz NOT NULL
);

-- Weekly counts kept after their facts expire (ADR-0024), without actors.
-- week is the Monday of the week in the deployment time zone; code is ''
-- when the facts carried none.
CREATE TABLE analytics.weekly_rollups (
    week       date    NOT NULL,
    event_type text    NOT NULL CHECK (event_type ~ '^[a-z]+\.[A-Za-z]{1,40}$'),
    code       text    NOT NULL DEFAULT '' CHECK (code = '' OR code ~ '^[a-z][a-z_]{0,31}$'),
    facts      integer NOT NULL CHECK (facts >= 0),
    members    integer NOT NULL CHECK (members >= 0),
    PRIMARY KEY (week, event_type, code)
);
