-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS assistant;
CREATE TABLE assistant.messages (
    id              uuid        PRIMARY KEY,
    member_id       uuid        NOT NULL,
    role            text        NOT NULL CHECK (role IN ('member', 'assistant')),
    reply_to        uuid,
    text_sealed     bytea       CHECK (octet_length(text_sealed) <= 16384),
    crisis_flag     boolean     NOT NULL DEFAULT false,
    degraded        boolean     NOT NULL DEFAULT false,
    sources         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    suggest         text        NOT NULL DEFAULT 'none'
                                CHECK (suggest IN ('none', 'tea', 'psychologist', 'rights', 'practice')),
    escalation_id   uuid,
    idempotency_key uuid,
    helped          boolean,
    rated_at        timestamptz,
    created_at      timestamptz NOT NULL,
    CHECK ((role = 'member') = (idempotency_key IS NOT NULL)),
    CHECK ((role = 'assistant') = (reply_to IS NOT NULL))
);
CREATE UNIQUE INDEX messages_member_idempotency_uidx
    ON assistant.messages (member_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX messages_reply_to_uidx
    ON assistant.messages (reply_to) WHERE reply_to IS NOT NULL;
CREATE INDEX messages_member_created_idx
    ON assistant.messages (member_id, created_at DESC, id DESC);
CREATE INDEX messages_created_idx
    ON assistant.messages (created_at);
