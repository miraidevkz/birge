-- Desired schema of the chat bounded context (declarative). See ADR-0006,
-- ADR-0009, ADR-0016 and ADR-0019. Message texts are sealed with the
-- data key of the author (chat.messages.text|<message id>), so deleting her
-- account crypto-shreds them, everything else is IDs, codes and times.
-- Rows are never deleted: members leave (left_at), reports close
-- (closed_at), and retention clears the text of a message (purged_at) while the
-- row stays as a "message deleted" tombstone so per-room sequences stay
-- gap-free. The table has no foreign key into another context.

CREATE SCHEMA IF NOT EXISTS chat;

-- One room. Group rooms (district, topic) are created from reference data
-- (EnsureDistrictRoom / EnsureTopicRoom), direct rooms by the consumers of
-- circle.MatchAccepted, emergency.SosResponseAccepted and
-- sharing.ContactAccepted.
CREATE TABLE chat.rooms (
    id                uuid        PRIMARY KEY,
    kind              text        NOT NULL CHECK (kind IN ('district', 'topic', 'direct')),
    -- district rooms: the membership district id (no foreign key across contexts)
    district_id       uuid,
    -- topic rooms: the membership needs code (special-needs type)
    topic_code        text        CHECK (topic_code ~ '^[a-z_]{2,40}$'),
    -- group rooms: the locale key of the title (UI strings live in locale files)
    title_key         text        CHECK (title_key ~ '^[a-z0-9_.]{3,80}$'),
    -- direct rooms: what opened the room and the pair (member_low < member_high)
    origin            text        CHECK (origin IN ('circle.match', 'emergency.sos', 'sharing.listing')),
    origin_id         uuid,
    member_low        uuid,
    member_high       uuid,
    slow_mode_seconds integer     NOT NULL DEFAULT 0 CHECK (slow_mode_seconds BETWEEN 0 AND 3600),
    -- Per-room message sequence: incremented by UPDATE ... RETURNING in the
    -- insert transaction, so the row lock serialises concurrent senders and
    -- a rolled-back insert gives its number back (gap-free).
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

-- One district room per district and one topic room per needs code.
CREATE UNIQUE INDEX rooms_district_uidx ON chat.rooms (district_id) WHERE kind = 'district';
CREATE UNIQUE INDEX rooms_topic_uidx ON chat.rooms (topic_code) WHERE kind = 'topic';
-- Natural key of a direct room: one per origin (match, SOS, listing) and pair,
-- so a redelivered or replayed event cannot open a second room.
CREATE UNIQUE INDEX rooms_direct_origin_uidx
    ON chat.rooms (origin, origin_id, member_low, member_high) WHERE kind = 'direct';

-- Membership of a room. Group rooms: the member joined herself, direct
-- rooms: both members are added when the room opens. Leaving sets left_at.
CREATE TABLE chat.room_members (
    room_id      uuid        NOT NULL REFERENCES chat.rooms (id),
    member_id    uuid        NOT NULL,
    joined_at    timestamptz NOT NULL,
    left_at      timestamptz,
    -- Per-room mute of push notifications (her own choice).
    muted        boolean     NOT NULL DEFAULT false,
    -- Last chat.new_message push for this room (push throttle).
    last_push_at timestamptz,
    -- Presence: a gateway connection subscribed to this room keeps this in
    -- the future (heartbeat), no push while it is.
    connected_until timestamptz,
    PRIMARY KEY (room_id, member_id)
);

CREATE INDEX room_members_member_idx ON chat.room_members (member_id) WHERE left_at IS NULL;

-- Moderation restrictions of a member in a room (ban, mute_1h).
CREATE TABLE chat.room_restrictions (
    room_id     uuid        NOT NULL REFERENCES chat.rooms (id),
    member_id   uuid        NOT NULL,
    banned_at   timestamptz,
    muted_until timestamptz,
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (room_id, member_id)
);

-- Mentors of a group room, assigned by staff, she must also hold the
-- membership role "mentor" at the time she moderates.
CREATE TABLE chat.room_mentors (
    room_id     uuid        NOT NULL REFERENCES chat.rooms (id),
    member_id   uuid        NOT NULL,
    assigned_by uuid        NOT NULL,
    assigned_at timestamptz NOT NULL,
    removed_at  timestamptz,
    PRIMARY KEY (room_id, member_id)
);

-- One message. The id is generated by the service before sealing, because
-- the associated data of the text names the row (chat.messages.text|<id>).
CREATE TABLE chat.messages (
    id            uuid        PRIMARY KEY,
    room_id       uuid        NOT NULL REFERENCES chat.rooms (id),
    seq           bigint      NOT NULL CHECK (seq >= 1),
    author_id     uuid        NOT NULL,
    -- Client-generated UUIDv7: a repeated send has one effect.
    client_id     uuid        NOT NULL,
    -- Personal data: the text sealed with the key of the author (associated
    -- data chat.messages.text|<id>: the AD field name is part of the schema
    -- and does not follow the column name), NULL once purged by retention.
    body          bytea       CHECK (octet_length(body) BETWEEN 1 AND 16384),
    status        text        NOT NULL CHECK (status IN ('visible', 'held', 'hidden')),
    hold_reason   text        CHECK (hold_reason IN ('crisis', 'newcomer', 'link', 'phone')),
    -- The safety escalation opened for a crisis message.
    escalation_id uuid,
    -- Kept outside retention until released by staff (ADR-0019).
    legal_hold    boolean     NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    purged_at     timestamptz,
    CONSTRAINT messages_room_seq_key UNIQUE (room_id, seq),
    CONSTRAINT messages_author_client_key UNIQUE (author_id, client_id),
    CONSTRAINT messages_purge_check CHECK ((purged_at IS NULL) = (body IS NOT NULL)),
    CONSTRAINT messages_hold_check CHECK (status <> 'held' OR hold_reason IS NOT NULL)
);

-- Rate limit and newcomer count per author.
CREATE INDEX messages_author_created_idx ON chat.messages (author_id, created_at);
-- Slow mode: the last message of the author in a room.
CREATE INDEX messages_room_author_created_idx ON chat.messages (room_id, author_id, created_at DESC);
-- Retention sweep.
CREATE INDEX messages_retention_idx ON chat.messages (created_at) WHERE purged_at IS NULL;

-- A report by a member of a message, one per reporter and message.
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

-- Retention keeps messages with an open report.
CREATE INDEX reports_open_idx ON chat.reports (message_id) WHERE closed_at IS NULL;
