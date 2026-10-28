-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE TABLE emergency.sos_links (
    id         uuid        PRIMARY KEY,
    token_hash bytea       NOT NULL CHECK (octet_length(token_hash) = 32),
    sos_id     uuid        NOT NULL REFERENCES emergency.sos_requests (id),
    contact_id uuid        NOT NULL REFERENCES emergency.trusted_contacts (id),
    owner_id   uuid        NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CONSTRAINT sos_links_expiry_check CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX sos_links_token_hash_uidx ON emergency.sos_links (token_hash);
CREATE INDEX sos_links_sos_contact_active_idx
    ON emergency.sos_links (sos_id, contact_id) WHERE revoked_at IS NULL;
CREATE INDEX sos_links_owner_idx ON emergency.sos_links (owner_id);
