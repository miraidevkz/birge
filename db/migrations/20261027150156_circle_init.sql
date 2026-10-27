-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS circle;
CREATE TABLE circle.tea_requests (
    id              uuid        PRIMARY KEY,
    member_id       uuid        NOT NULL,
    idempotency_key uuid        NOT NULL,
    format          text        NOT NULL
                                CHECK (format IN ('meeting', 'call')),
    same_district   boolean     NOT NULL,
    similar_age     boolean     NOT NULL,
    shared_needs    boolean     NOT NULL,
    status          text        NOT NULL
                                CHECK (status IN ('open', 'matched', 'cancelled', 'expired')),
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    CONSTRAINT tea_requests_expires_check CHECK (expires_at > created_at)
);
CREATE TABLE circle.matches (
    id           uuid        PRIMARY KEY,
    member_a     uuid        NOT NULL,
    member_b     uuid        NOT NULL,
    request_a_id uuid        NOT NULL REFERENCES circle.tea_requests (id),
    request_b_id uuid        NOT NULL REFERENCES circle.tea_requests (id),
    format       text        NOT NULL
                             CHECK (format IN ('meeting', 'call')),
    answer_a     boolean,
    answer_b     boolean,
    met_a        boolean,
    met_b        boolean,
    status       text        NOT NULL
                             CHECK (status IN ('proposed', 'accepted', 'declined', 'met', 'not_met', 'expired')),
    room_id      uuid,
    ask_met_at   timestamptz,
    created_at   timestamptz NOT NULL,
    CONSTRAINT matches_members_order CHECK (member_a < member_b),
    CONSTRAINT matches_distinct_members CHECK (member_a <> member_b),
    CONSTRAINT matches_distinct_requests CHECK (request_a_id <> request_b_id)
);
CREATE TABLE circle.acquaintances (
    member_a uuid        NOT NULL,
    member_b uuid        NOT NULL,
    since    timestamptz NOT NULL,
    PRIMARY KEY (member_a, member_b),
    CONSTRAINT acquaintances_members_order CHECK (member_a < member_b),
    CONSTRAINT acquaintances_distinct_members CHECK (member_a <> member_b)
);
CREATE UNIQUE INDEX tea_requests_member_idempotency_uidx
    ON circle.tea_requests (member_id, idempotency_key);
CREATE UNIQUE INDEX tea_requests_one_open_uidx
    ON circle.tea_requests (member_id)
    WHERE status = 'open';
CREATE INDEX tea_requests_open_idx
    ON circle.tea_requests (created_at ASC, id ASC)
    WHERE status = 'open';
CREATE INDEX tea_requests_member_created_idx
    ON circle.tea_requests (member_id, created_at DESC, id DESC);
CREATE INDEX matches_member_a_idx
    ON circle.matches (member_a, created_at DESC, id DESC);
CREATE INDEX matches_member_b_idx
    ON circle.matches (member_b, created_at DESC, id DESC);
CREATE INDEX matches_pair_created_idx
    ON circle.matches (member_a, member_b, created_at DESC);
CREATE INDEX acquaintances_member_b_idx
    ON circle.acquaintances (member_b, since DESC);
