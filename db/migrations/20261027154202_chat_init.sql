-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS chat;
CREATE TABLE chat.rooms (
    id                uuid        PRIMARY KEY,
    kind              text        NOT NULL CHECK (kind IN ('district', 'topic', 'direct')),
    district_id       uuid,
    topic_code        text        CHECK (topic_code ~ '^[a-z_]{2,40}$'),
    title_key         text        CHECK (title_key ~ '^[a-z0-9_.]{3,80}$'),
    origin            text        CHECK (origin IN ('circle.match', 'emergency.sos', 'sharing.listing')),
    origin_id         uuid,
    member_low        uuid,
    member_high       uuid,
    slow_mode_seconds integer     NOT NULL DEFAULT 0 CHECK (slow_mode_seconds BETWEEN 0 AND 3600),
    last_seq          bigint      NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
    created_at        timestamptz NOT NULL,
    CONSTRAINT rooms_kind_shape_check CHECK (
        (kind = 'district' AND district_id IS NOT NULL AND topic_code IS NULL AND title_key IS NOT NULL
            AND origin IS NULL AND origin_id IS NULL AND member_low IS NULL AND member_high IS NULL)
        OR (kind = 'topic' AND topic_code IS NOT NULL AND district_id IS NULL AND title_key IS NOT NULL
            AND origin IS NULL AND origin_id IS NULL AND member_low IS NULL AND member_high IS NULL)
        OR (kind = 'direct' AND district_id IS NULL AND topic_code IS NULL AND title_key IS NULL
            AND origin IS NOT NULL AND origin_id IS NOT NULL AND member_low IS NOT NULL AND member_high IS NOT NULL
            AND member_low < member_high)
    )
);
CREATE TABLE chat.room_members (
    room_id      uuid        NOT NULL REFERENCES chat.rooms (id),
    member_id    uuid        NOT NULL,
    joined_at    timestamptz NOT NULL,
    left_at      timestamptz,
    muted        boolean     NOT NULL DEFAULT false,
    last_push_at timestamptz,
    connected_until timestamptz,
    PRIMARY KEY (room_id, member_id)
);
CREATE TABLE chat.room_restrictions (
    room_id     uuid        NOT NULL REFERENCES chat.rooms (id),
    member_id   uuid        NOT NULL,
    banned_at   timestamptz,
    muted_until timestamptz,
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (room_id, member_id)
);
CREATE TABLE chat.room_mentors (
    room_id     uuid        NOT NULL REFERENCES chat.rooms (id),
    member_id   uuid        NOT NULL,
    assigned_by uuid        NOT NULL,
    assigned_at timestamptz NOT NULL,
    removed_at  timestamptz,
    PRIMARY KEY (room_id, member_id)
);
CREATE TABLE chat.messages (
    id            uuid        PRIMARY KEY,
    room_id       uuid        NOT NULL REFERENCES chat.rooms (id),
    seq           bigint      NOT NULL CHECK (seq >= 1),
    author_id     uuid        NOT NULL,
    client_id     uuid        NOT NULL,
    body          bytea       CHECK (octet_length(body) BETWEEN 1 AND 16384),
    status        text        NOT NULL CHECK (status IN ('visible', 'held', 'hidden')),
    hold_reason   text        CHECK (hold_reason IN ('crisis', 'newcomer', 'link', 'phone')),
    escalation_id uuid,
    legal_hold    boolean     NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    purged_at     timestamptz,
    CONSTRAINT messages_room_seq_key UNIQUE (room_id, seq),
    CONSTRAINT messages_author_client_key UNIQUE (author_id, client_id),
    CONSTRAINT messages_purge_check CHECK ((purged_at IS NULL) = (body IS NOT NULL)),
    CONSTRAINT messages_hold_check CHECK (status <> 'held' OR hold_reason IS NOT NULL)
);
CREATE TABLE chat.reports (
    id          uuid        PRIMARY KEY,
    message_id  uuid        NOT NULL REFERENCES chat.messages (id),
    room_id     uuid        NOT NULL REFERENCES chat.rooms (id),
    reporter_id uuid        NOT NULL,
    reason      text        NOT NULL CHECK (reason ~ '^[a-z_]{2,40}$'),
    created_at  timestamptz NOT NULL,
    closed_at   timestamptz,
    CONSTRAINT reports_message_reporter_key UNIQUE (message_id, reporter_id)
);
CREATE UNIQUE INDEX rooms_district_uidx ON chat.rooms (district_id) WHERE kind = 'district';
CREATE UNIQUE INDEX rooms_topic_uidx ON chat.rooms (topic_code) WHERE kind = 'topic';
CREATE UNIQUE INDEX rooms_direct_origin_uidx
    ON chat.rooms (origin, origin_id, member_low, member_high) WHERE kind = 'direct';
CREATE INDEX room_members_member_idx ON chat.room_members (member_id) WHERE left_at IS NULL;
CREATE INDEX messages_author_created_idx ON chat.messages (author_id, created_at);
CREATE INDEX messages_room_author_created_idx ON chat.messages (room_id, author_id, created_at DESC);
CREATE INDEX messages_retention_idx ON chat.messages (created_at) WHERE purged_at IS NULL;
CREATE INDEX reports_open_idx ON chat.reports (message_id) WHERE closed_at IS NULL;
