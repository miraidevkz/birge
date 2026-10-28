-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE TABLE platform.staff_accounts (
    staff_id      uuid        PRIMARY KEY,
    display_name  text        CHECK (char_length(display_name) BETWEEN 1 AND 80),
    roles         text[]      NOT NULL DEFAULT '{}'
        CHECK (roles <@ ARRAY['psychologist', 'moderator', 'lawyer', 'admin']::text[]),
    first_seen_at timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL
);
CREATE INDEX audit_log_occurred_idx ON platform.audit_log (occurred_at DESC, id DESC);
