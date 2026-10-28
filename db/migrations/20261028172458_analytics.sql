-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS analytics;
CREATE TABLE analytics.facts (
    id          uuid        PRIMARY KEY,
    event_type  text        NOT NULL CHECK (event_type ~ '^[a-z]+\.[A-Za-z]{1,40}$'),
    occurred_at timestamptz NOT NULL,
    code        text        CHECK (code IS NULL OR code ~ '^[a-z][a-z_]{0,31}$'),
    actor       bytea       CHECK (actor IS NULL OR octet_length(actor) = 32),
    subject     bytea       CHECK (subject IS NULL OR octet_length(subject) = 32)
);
CREATE TABLE analytics.erased_actors (
    actor     bytea       PRIMARY KEY CHECK (octet_length(actor) = 32),
    erased_at timestamptz NOT NULL
);
CREATE TABLE analytics.weekly_rollups (
    week       date    NOT NULL,
    event_type text    NOT NULL CHECK (event_type ~ '^[a-z]+\.[A-Za-z]{1,40}$'),
    code       text    NOT NULL DEFAULT '' CHECK (code = '' OR code ~ '^[a-z][a-z_]{0,31}$'),
    facts      integer NOT NULL CHECK (facts >= 0),
    members    integer NOT NULL CHECK (members >= 0),
    PRIMARY KEY (week, event_type, code)
);
CREATE INDEX facts_type_time_idx ON analytics.facts (event_type, occurred_at);
CREATE INDEX facts_actor_idx ON analytics.facts (actor) WHERE actor IS NOT NULL;
CREATE INDEX facts_subject_idx ON analytics.facts (subject, event_type) WHERE subject IS NOT NULL;
