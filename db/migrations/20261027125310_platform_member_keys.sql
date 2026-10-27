-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE TABLE platform.member_keys (
    member_id    uuid        PRIMARY KEY,
    wrapped_dek  bytea       NOT NULL,
    kek_id       text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    destroyed_at timestamptz,
    CONSTRAINT member_keys_tombstone_check
        CHECK ((destroyed_at IS NULL) = (octet_length(wrapped_dek) > 0))
);
