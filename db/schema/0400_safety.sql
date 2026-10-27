-- Desired schema of the safety bounded context (declarative). See ADR-0006 and
-- ADR-0023. Escalations carry no name or phone: the member is an ID, the note
-- is sealed with the member's data key (ADR-0009).

CREATE SCHEMA IF NOT EXISTS safety;

-- A request for a person: open -> taken -> closed. Rows are never deleted.
CREATE TABLE safety.escalations (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    member_id   uuid        NOT NULL,
    source      text        NOT NULL
                            CHECK (source IN ('assistant', 'checkin', 'sos', 'menu', 'chat')),
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

-- At most one open-or-taken escalation per member: OpenEscalation relies on it
-- for idempotency under concurrent requests.
CREATE UNIQUE INDEX escalations_active_member_idx
    ON safety.escalations (member_id) WHERE status IN ('open', 'taken');

-- Staff queue, newest first, optionally filtered by status.
CREATE INDEX escalations_queue_idx ON safety.escalations (opened_at DESC, id DESC);
CREATE INDEX escalations_status_queue_idx ON safety.escalations (status, opened_at DESC, id DESC);

-- Duty roster. The id is the client's Idempotency-Key (UUIDv7). The on-duty
-- phone is sealed with the staff member's data key and never returned by the
-- API. Shifts are cancelled (cancelled_at), never deleted.
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

CREATE INDEX duty_shifts_cover_idx
    ON safety.duty_shifts (role, backup, starts_at, ends_at) WHERE cancelled_at IS NULL;
CREATE INDEX duty_shifts_period_idx
    ON safety.duty_shifts (starts_at, ends_at) WHERE cancelled_at IS NULL;
