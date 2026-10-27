-- Desired schema of the directory bounded context (declarative). See ADR-0019.
-- Specialists and reviews suggested by verified members; both go through
-- moderation. One review per member per specialist (resubmit replaces and
-- returns to pending). Rows are never deleted: status changes only; the only
-- deletion path stays the account-deletion flow.

CREATE SCHEMA IF NOT EXISTS directory;

-- One specialist card. Contacts are public work contacts of the specialist
-- or organisation (not a mother's personal contacts).
CREATE TABLE directory.specialists (
    id              uuid        PRIMARY KEY,
    added_by        uuid        NOT NULL,
    idempotency_key uuid        NOT NULL,
    name            text        NOT NULL
                                CHECK (char_length(name) BETWEEN 2 AND 120),
    profession      text        NOT NULL
                                CHECK (profession ~ '^[a-z_]{2,40}$'),
    district_id     uuid        NOT NULL,
    needs           text[]      NOT NULL DEFAULT '{}'
                                CHECK (cardinality(needs) <= 10),
    contacts        text        CHECK (contacts IS NULL OR char_length(contacts) <= 300),
    status          text        NOT NULL
                                CHECK (status IN ('pending', 'active', 'rejected')),
    reject_reason   text        CHECK (reject_reason IS NULL OR char_length(reject_reason) <= 40),
    created_at      timestamptz NOT NULL,
    CONSTRAINT specialists_reject_check CHECK (
        (status = 'rejected' AND reject_reason IS NOT NULL)
        OR (status <> 'rejected' AND reject_reason IS NULL)
    )
);

CREATE UNIQUE INDEX specialists_added_by_idempotency_uidx
    ON directory.specialists (added_by, idempotency_key);
CREATE INDEX specialists_search_idx
    ON directory.specialists (created_at DESC, id DESC)
    WHERE status = 'active';
CREATE INDEX specialists_profession_idx
    ON directory.specialists (profession, created_at DESC, id DESC)
    WHERE status = 'active';
CREATE INDEX specialists_district_idx
    ON directory.specialists (district_id, created_at DESC, id DESC)
    WHERE status = 'active';
CREATE INDEX specialists_needs_gin_idx
    ON directory.specialists USING gin (needs)
    WHERE status = 'active';

-- One review per (specialist, author). Resubmitting replaces the row and
-- returns it to pending for moderation.
CREATE TABLE directory.reviews (
    id            uuid        PRIMARY KEY,
    specialist_id uuid        NOT NULL REFERENCES directory.specialists (id),
    author_id     uuid        NOT NULL,
    rating        integer     NOT NULL CHECK (rating BETWEEN 1 AND 5),
    content       text        NOT NULL
                              CHECK (char_length(content) BETWEEN 1 AND 1000),
    status        text        NOT NULL
                              CHECK (status IN ('pending', 'active', 'rejected')),
    reject_reason text        CHECK (reject_reason IS NULL OR char_length(reject_reason) <= 40),
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    CONSTRAINT reviews_reject_check CHECK (
        (status = 'rejected' AND reject_reason IS NOT NULL)
        OR (status <> 'rejected' AND reject_reason IS NULL)
    ),
    CONSTRAINT reviews_specialist_author_key UNIQUE (specialist_id, author_id)
);

CREATE INDEX reviews_specialist_active_idx
    ON directory.reviews (specialist_id, created_at DESC, id DESC)
    WHERE status = 'active';
