-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS wellbeing;
CREATE TABLE wellbeing.schedules (
    member_id  uuid        PRIMARY KEY,
    weekday    integer     NOT NULL CHECK (weekday BETWEEN 1 AND 7),
    hour       integer     NOT NULL CHECK (hour BETWEEN 0 AND 23),
    enabled    boolean     NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE wellbeing.checkins (
    id                    uuid        PRIMARY KEY,
    member_id             uuid        NOT NULL,
    questionnaire_version text        NOT NULL
                                      CHECK (char_length(questionnaire_version) BETWEEN 1 AND 32),
    answers               bytea       NOT NULL CHECK (octet_length(answers) <= 8192),
    phq4                  integer     NOT NULL CHECK (phq4 BETWEEN 0 AND 12),
    pss4                  integer     NOT NULL CHECK (pss4 BETWEEN 0 AND 16),
    flag                  text        NOT NULL CHECK (flag IN ('none', 'elevated', 'crisis')),
    idempotency_key       uuid        NOT NULL,
    completed_at          timestamptz NOT NULL
);
CREATE TABLE wellbeing.practices (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    lang             text        NOT NULL CHECK (lang IN ('kk', 'ru')),
    title_key        text        NOT NULL CHECK (char_length(title_key) BETWEEN 1 AND 80),
    duration_seconds integer     NOT NULL CHECK (duration_seconds BETWEEN 1 AND 3600),
    object_key       text        NOT NULL CHECK (char_length(object_key) BETWEEN 1 AND 512),
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX checkins_member_idempotency_uidx
    ON wellbeing.checkins (member_id, idempotency_key);
CREATE INDEX checkins_member_completed_idx
    ON wellbeing.checkins (member_id, completed_at ASC, id ASC);
CREATE INDEX practices_lang_idx ON wellbeing.practices (lang, created_at ASC, id ASC);
