-- Desired schema of the membership context (declarative; ADR-0006): members,
-- their children, verification requests, blocks (ADR-0019) and reference data.
-- Only internal/domains/membership reads or writes these tables.

CREATE SCHEMA IF NOT EXISTS membership;

-- Reference data of the deployment. Codes are locale keys; labels live in the
-- client locale files.
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

-- A member is created lazily on the first authenticated request of an identity
-- (ADR-0008 addendum); identity_id is unique so concurrent first calls create one row.
CREATE TABLE membership.members (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    identity_id  uuid        NOT NULL UNIQUE,
    -- Personal data: sealed with the member's key through the Encryptor port
    -- (ADR-0009). NULL until the member sets a name.
    display_name bytea       CHECK (octet_length(display_name) <= 1024),
    lang         text        NOT NULL DEFAULT 'kk' CHECK (lang IN ('kk', 'ru', 'en')),
    district_id  uuid        REFERENCES membership.districts (id),
    verification text        NOT NULL DEFAULT 'new' CHECK (verification IN ('new', 'pending', 'verified')),
    verified_at  timestamptz,
    roles        text[]      NOT NULL DEFAULT '{mom}'
        CHECK (roles <@ ARRAY['mom', 'mentor', 'psychologist', 'lawyer', 'moderator', 'admin']::text[]),
    -- Staff suspension from moderation.block_author (ADR-0019): session refused
    -- and mutual invisibility like a block. Both set or both null.
    suspended_at     timestamptz,
    suspended_reason text
        CHECK (suspended_reason IS NULL OR suspended_reason ~ '^[a-z][a-z0-9_]{0,39}$'),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    -- Set by account deletion: the row stays as a tombstone with id and
    -- identity_id only, so the identity is refused and never gets a new member.
    deleted_at   timestamptz,
    CHECK ((suspended_at IS NULL) = (suspended_reason IS NULL))
);

CREATE INDEX members_district_idx ON membership.members (district_id);
-- Staff member list indexes: keyset order (newest verified/suspended first).
CREATE INDEX members_staff_verified_idx ON membership.members (verified_at DESC, id DESC)
    WHERE verification = 'verified' AND deleted_at IS NULL;
CREATE INDEX members_staff_verified_district_idx ON membership.members (district_id, verified_at DESC, id DESC)
    WHERE verification = 'verified' AND deleted_at IS NULL;
CREATE INDEX members_staff_suspended_idx ON membership.members (suspended_at DESC, id DESC)
    WHERE suspended_at IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX members_staff_suspended_district_idx ON membership.members (district_id, suspended_at DESC, id DESC)
    WHERE suspended_at IS NOT NULL AND deleted_at IS NULL;

-- Children carry no names: birth year and needs codes only.
CREATE TABLE membership.children (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    member_id       uuid        NOT NULL REFERENCES membership.members (id) ON DELETE CASCADE,
    -- Idempotency-Key of the request that added the child.
    idempotency_key uuid        NOT NULL,
    birth_year      integer     NOT NULL CHECK (birth_year BETWEEN 1990 AND 2100),
    needs           text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(needs) <= 10),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (member_id, idempotency_key)
);

CREATE TABLE membership.verification_requests (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    member_id     uuid        NOT NULL REFERENCES membership.members (id) ON DELETE CASCADE,
    status        text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    -- Rejection reason code from moderation.Decided; null while pending or approved.
    reject_reason text
        CHECK (reject_reason IS NULL OR reject_reason ~ '^[a-z][a-z0-9_]{0,39}$'),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX verification_requests_member_idx ON membership.verification_requests (member_id, created_at);
-- At most one pending request per member.
CREATE UNIQUE INDEX verification_requests_pending_idx ON membership.verification_requests (member_id) WHERE status = 'pending';

-- Blocks (ADR-0019): mutual invisibility across contexts, read in either direction.
CREATE TABLE membership.blocks (
    blocker uuid        NOT NULL REFERENCES membership.members (id) ON DELETE CASCADE,
    blocked uuid        NOT NULL REFERENCES membership.members (id) ON DELETE CASCADE,
    since   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker, blocked),
    CHECK (blocker <> blocked)
);

CREATE INDEX blocks_blocked_idx ON membership.blocks (blocked);
