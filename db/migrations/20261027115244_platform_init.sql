-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS platform;
CREATE TABLE platform.outbox (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    event_type    text        NOT NULL CHECK (event_type ~ '^[a-z]+\.[A-Z][A-Za-z]+$'),
    event_version integer     NOT NULL CHECK (event_version >= 1),
    payload       jsonb       NOT NULL,
    trace_parent  text,
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    published_at  timestamptz
);
CREATE TABLE platform.processed_events (
    consumer     text        NOT NULL,
    event_id     uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
CREATE TABLE platform.timers (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    kind         text        NOT NULL,
    subject_id   uuid        NOT NULL,
    dedup_key    text,
    due_at       timestamptz NOT NULL,
    payload      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    attempts     integer     NOT NULL DEFAULT 0,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    done_at      timestamptz
);
CREATE TABLE platform.audit_log (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    actor_id    uuid        NOT NULL,
    actor_role  text        NOT NULL,
    action      text        NOT NULL,
    object_type text        NOT NULL,
    object_id   uuid,
    reason      text
);
CREATE INDEX outbox_unpublished_idx ON platform.outbox (occurred_at) WHERE published_at IS NULL;
CREATE INDEX timers_due_idx ON platform.timers (due_at) WHERE done_at IS NULL;
CREATE UNIQUE INDEX timers_active_dedup_idx ON platform.timers (kind, dedup_key) WHERE done_at IS NULL AND dedup_key IS NOT NULL;
CREATE INDEX audit_log_object_idx ON platform.audit_log (object_type, object_id, occurred_at);
CREATE INDEX audit_log_actor_idx ON platform.audit_log (actor_id, occurred_at);
