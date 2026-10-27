-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS emergency;
CREATE TABLE emergency.sos_requests (
    id              uuid        PRIMARY KEY,
    member_id       uuid        NOT NULL,
    idempotency_key uuid        NOT NULL,
    type            text        NOT NULL
                                CHECK (type IN ('childcare', 'item_or_medicine', 'other')),
    content         bytea       NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 4096),
    status          text        NOT NULL DEFAULT 'open'
                                CHECK (status IN ('open', 'closed')),
    created_at      timestamptz NOT NULL,
    closes_at       timestamptz NOT NULL,
    district_id     uuid,
    shared_at       timestamptz,
    closed_at       timestamptz,
    close_reason    text        CHECK (close_reason IN ('author', 'auto')),
    CONSTRAINT sos_requests_closes_check CHECK (closes_at > created_at),
    CONSTRAINT sos_requests_shared_check CHECK ((district_id IS NULL) = (shared_at IS NULL)),
    CONSTRAINT sos_requests_childcare_never_shared CHECK (type <> 'childcare' OR shared_at IS NULL),
    CONSTRAINT sos_requests_lifecycle_check CHECK (
        (status = 'open' AND closed_at IS NULL AND close_reason IS NULL)
        OR (status = 'closed' AND closed_at IS NOT NULL AND close_reason IS NOT NULL)
    )
);
CREATE TABLE emergency.sos_responses (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    sos_id       uuid        NOT NULL REFERENCES emergency.sos_requests (id),
    responder_id uuid        NOT NULL,
    created_at   timestamptz NOT NULL,
    accepted_at  timestamptz,
    CONSTRAINT sos_responses_sos_responder_key UNIQUE (sos_id, responder_id)
);
CREATE TABLE emergency.child_cards (
    member_id    uuid        PRIMARY KEY,
    content      bytea       NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 16384),
    updated_at   timestamptz NOT NULL,
    last_sent_at timestamptz
);
CREATE TABLE emergency.trusted_contacts (
    id                uuid        PRIMARY KEY,
    owner_id          uuid        NOT NULL,
    idempotency_key   uuid        NOT NULL,
    kind              text        NOT NULL CHECK (kind IN ('member', 'external')),
    contact_member_id uuid,
    name              bytea       CHECK (octet_length(name) <= 1024),
    phone             bytea       CHECK (octet_length(phone) <= 1024),
    lang              text        NOT NULL CHECK (lang IN ('kk', 'ru')),
    status            text        NOT NULL CHECK (status IN ('invited', 'confirmed')),
    created_at        timestamptz NOT NULL,
    confirmed_at      timestamptz,
    plan_sent_at      timestamptz,
    removed_at        timestamptz,
    CONSTRAINT trusted_contacts_kind_fields_check CHECK (
        (kind = 'member' AND contact_member_id IS NOT NULL AND name IS NULL AND phone IS NULL)
        OR (kind = 'external' AND contact_member_id IS NULL AND name IS NOT NULL AND phone IS NOT NULL
            AND status = 'confirmed')
    ),
    CONSTRAINT trusted_contacts_confirmed_check CHECK ((status = 'confirmed') = (confirmed_at IS NOT NULL)),
    CONSTRAINT trusted_contacts_self_check CHECK (contact_member_id IS NULL OR contact_member_id <> owner_id)
);
CREATE TABLE emergency.plan_sends (
    id              uuid        PRIMARY KEY,
    member_id       uuid        NOT NULL,
    idempotency_key uuid        NOT NULL,
    contact_count   integer     NOT NULL CHECK (contact_count BETWEEN 1 AND 3),
    sent_at         timestamptz NOT NULL
);
CREATE UNIQUE INDEX sos_requests_member_idempotency_uidx
    ON emergency.sos_requests (member_id, idempotency_key);
CREATE INDEX sos_requests_member_created_idx
    ON emergency.sos_requests (member_id, created_at DESC, id DESC);
CREATE INDEX sos_requests_feed_idx
    ON emergency.sos_requests (district_id, created_at DESC, id DESC)
    WHERE status = 'open' AND shared_at IS NOT NULL AND type <> 'childcare';
CREATE INDEX sos_responses_sos_created_idx
    ON emergency.sos_responses (sos_id, created_at ASC, id ASC);
CREATE UNIQUE INDEX trusted_contacts_owner_idempotency_uidx
    ON emergency.trusted_contacts (owner_id, idempotency_key);
CREATE UNIQUE INDEX trusted_contacts_owner_member_uidx
    ON emergency.trusted_contacts (owner_id, contact_member_id)
    WHERE removed_at IS NULL AND kind = 'member';
CREATE INDEX trusted_contacts_owner_active_idx
    ON emergency.trusted_contacts (owner_id, created_at ASC, id ASC) WHERE removed_at IS NULL;
CREATE UNIQUE INDEX plan_sends_member_idempotency_uidx
    ON emergency.plan_sends (member_id, idempotency_key);
CREATE INDEX plan_sends_member_sent_idx
    ON emergency.plan_sends (member_id, sent_at DESC);
