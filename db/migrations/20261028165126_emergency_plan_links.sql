-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE TABLE emergency.plan_links (
    id         uuid        PRIMARY KEY,
    token_hash bytea       NOT NULL CHECK (octet_length(token_hash) = 32),
    contact_id uuid        NOT NULL REFERENCES emergency.trusted_contacts (id),
    owner_id   uuid        NOT NULL,
    sent_at    timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CONSTRAINT plan_links_expiry_check CHECK (expires_at > sent_at)
);
CREATE UNIQUE INDEX plan_links_token_hash_uidx ON emergency.plan_links (token_hash);
CREATE INDEX plan_links_contact_active_idx
    ON emergency.plan_links (contact_id) WHERE revoked_at IS NULL;
CREATE INDEX plan_links_owner_idx ON emergency.plan_links (owner_id);
