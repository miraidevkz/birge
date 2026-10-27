-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS privacy;
CREATE TABLE privacy.consent_history (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    member_id  uuid        NOT NULL,
    type       text        NOT NULL
                           CHECK (type IN ('data_processing', 'ai_crossborder', 'research')),
    version    text        NOT NULL
                           CHECK (char_length(version) BETWEEN 1 AND 32),
    granted    boolean     NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE privacy.deletion_requests (
    member_id    uuid        PRIMARY KEY,
    requested_at timestamptz NOT NULL,
    due_at       timestamptz NOT NULL,
    completed_at timestamptz
);
CREATE INDEX consent_history_member_type_idx
    ON privacy.consent_history (member_id, type, changed_at DESC, id DESC);
