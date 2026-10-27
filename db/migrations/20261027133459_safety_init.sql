-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS safety;
CREATE TABLE safety.escalations (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    member_id   uuid        NOT NULL,
    source      text        NOT NULL
                            CHECK (source IN ('assistant', 'checkin', 'sos', 'menu')),
    status      text        NOT NULL DEFAULT 'open'
                            CHECK (status IN ('open', 'taken', 'closed')),
    district_id uuid,
    opened_at   timestamptz NOT NULL,
    taken_by    uuid,
    taken_at    timestamptz,
    outcome     text
                            CHECK (outcome IN ('contacted', 'referred', 'emergency_services', 'no_answer', 'not_a_crisis')),
    note        bytea,
    closed_at   timestamptz,
    CONSTRAINT escalations_lifecycle_check CHECK (
        (status = 'open'
            AND taken_by IS NULL AND taken_at IS NULL
            AND outcome IS NULL AND note IS NULL AND closed_at IS NULL)
        OR (status = 'taken'
            AND taken_by IS NOT NULL AND taken_at IS NOT NULL
            AND outcome IS NULL AND note IS NULL AND closed_at IS NULL)
        OR (status = 'closed'
            AND taken_by IS NOT NULL AND taken_at IS NOT NULL
            AND outcome IS NOT NULL AND closed_at IS NOT NULL)
    )
);
CREATE TABLE safety.duty_shifts (
    id           uuid        PRIMARY KEY,
    staff_id     uuid        NOT NULL,
    role         text        NOT NULL CHECK (role IN ('psychologist', 'moderator')),
    starts_at    timestamptz NOT NULL,
    ends_at      timestamptz NOT NULL,
    backup       boolean     NOT NULL DEFAULT false,
    phone        bytea       NOT NULL,
    created_at   timestamptz NOT NULL,
    cancelled_at timestamptz,
    CONSTRAINT duty_shifts_period_check CHECK (ends_at > starts_at)
);
CREATE UNIQUE INDEX escalations_active_member_idx
    ON safety.escalations (member_id) WHERE status IN ('open', 'taken');
CREATE INDEX escalations_queue_idx ON safety.escalations (opened_at DESC, id DESC);
CREATE INDEX escalations_status_queue_idx ON safety.escalations (status, opened_at DESC, id DESC);
CREATE INDEX duty_shifts_cover_idx
    ON safety.duty_shifts (role, backup, starts_at, ends_at) WHERE cancelled_at IS NULL;
CREATE INDEX duty_shifts_period_idx
    ON safety.duty_shifts (starts_at, ends_at) WHERE cancelled_at IS NULL;
