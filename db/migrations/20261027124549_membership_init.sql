-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS membership;
CREATE TABLE membership.cities (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    code       text        NOT NULL UNIQUE CHECK (code ~ '^[a-z_]{2,40}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE membership.districts (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    city_id    uuid        NOT NULL REFERENCES membership.cities (id),
    code       text        NOT NULL CHECK (code ~ '^[a-z_]{2,40}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (city_id, code)
);
CREATE TABLE membership.needs (
    code       text        PRIMARY KEY CHECK (code ~ '^[a-z_]{2,40}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE membership.members (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    identity_id  uuid        NOT NULL UNIQUE,
    display_name bytea       CHECK (octet_length(display_name) <= 1024),
    lang         text        NOT NULL DEFAULT 'kk' CHECK (lang IN ('kk', 'ru', 'en')),
    district_id  uuid        REFERENCES membership.districts (id),
    verification text        NOT NULL DEFAULT 'new' CHECK (verification IN ('new', 'pending', 'verified')),
    roles        text[]      NOT NULL DEFAULT '{mom}'
        CHECK (roles <@ ARRAY['mom', 'mentor', 'psychologist', 'lawyer', 'moderator', 'admin']::text[]),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE membership.children (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    member_id       uuid        NOT NULL REFERENCES membership.members (id) ON DELETE CASCADE,
    idempotency_key uuid        NOT NULL,
    birth_year      integer     NOT NULL CHECK (birth_year BETWEEN 1990 AND 2100),
    needs           text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(needs) <= 10),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (member_id, idempotency_key)
);
CREATE TABLE membership.verification_requests (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    member_id  uuid        NOT NULL REFERENCES membership.members (id) ON DELETE CASCADE,
    status     text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE membership.blocks (
    blocker uuid        NOT NULL REFERENCES membership.members (id) ON DELETE CASCADE,
    blocked uuid        NOT NULL REFERENCES membership.members (id) ON DELETE CASCADE,
    since   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker, blocked),
    CHECK (blocker <> blocked)
);
CREATE INDEX members_district_idx ON membership.members (district_id);
CREATE INDEX verification_requests_member_idx ON membership.verification_requests (member_id, created_at);
CREATE UNIQUE INDEX verification_requests_pending_idx ON membership.verification_requests (member_id) WHERE status = 'pending';
CREATE INDEX blocks_blocked_idx ON membership.blocks (blocked);
