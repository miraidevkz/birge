-- Desired schema of the circle bounded context (declarative). See ADR-0016,
-- ADR-0019 and ADR-0020. Tea matching and pairwise "met in person"
-- (acquaintances). Display names, districts, ages and needs are read from
-- membership at request time and never stored here. Rows are never deleted:
-- requests cancel or expire, matches change status; the only deletion path
-- stays the account-deletion flow.

CREATE SCHEMA IF NOT EXISTS circle;

-- One tea request. The member chooses format and hard preferences; matching
-- runs hourly (platform timer circle.MatchRun). Expires after the configured
-- delay (platform timer circle.RequestExpire).
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

-- Repeated creates with the same Idempotency-Key have one effect.
CREATE UNIQUE INDEX tea_requests_member_idempotency_uidx
    ON circle.tea_requests (member_id, idempotency_key);
-- At most one open request per member (§ 4.4).
CREATE UNIQUE INDEX tea_requests_one_open_uidx
    ON circle.tea_requests (member_id)
    WHERE status = 'open';
-- Matching loads open requests oldest first.
CREATE INDEX tea_requests_open_idx
    ON circle.tea_requests (created_at ASC, id ASC)
    WHERE status = 'open';
CREATE INDEX tea_requests_member_created_idx
    ON circle.tea_requests (member_id, created_at DESC, id DESC);

-- A proposed pair from two open requests. member_a < member_b (canonical).
-- Answers and met answers are per side; contacts are never stored.
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
    -- Filled when chat reports the private room after MatchAccepted.
    room_id      uuid,
    ask_met_at   timestamptz,
    created_at   timestamptz NOT NULL,
    CONSTRAINT matches_members_order CHECK (member_a < member_b),
    CONSTRAINT matches_distinct_members CHECK (member_a <> member_b),
    CONSTRAINT matches_distinct_requests CHECK (request_a_id <> request_b_id)
);

CREATE INDEX matches_member_a_idx
    ON circle.matches (member_a, created_at DESC, id DESC);
CREATE INDEX matches_member_b_idx
    ON circle.matches (member_b, created_at DESC, id DESC);
-- "Same pair within 30 days" lookup during matching.
CREATE INDEX matches_pair_created_idx
    ON circle.matches (member_a, member_b, created_at DESC);

-- Unordered pair stored canonically (member_a < member_b). Created when both
-- confirm they met; used by emergency as the Acquaintances port (§ 3).
CREATE TABLE circle.acquaintances (
    member_a uuid        NOT NULL,
    member_b uuid        NOT NULL,
    since    timestamptz NOT NULL,
    PRIMARY KEY (member_a, member_b),
    CONSTRAINT acquaintances_members_order CHECK (member_a < member_b),
    CONSTRAINT acquaintances_distinct_members CHECK (member_a <> member_b)
);

CREATE INDEX acquaintances_member_b_idx
    ON circle.acquaintances (member_b, since DESC);
