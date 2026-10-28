-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE TABLE platform.rate_counters (
    key          text        NOT NULL,
    window_start timestamptz NOT NULL,
    hits         bigint      NOT NULL CHECK (hits >= 1),
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (key, window_start)
);
CREATE TABLE platform.one_time_claims (
    key        text        PRIMARY KEY,
    expires_at timestamptz NOT NULL
);
CREATE INDEX rate_counters_expires_idx ON platform.rate_counters (expires_at);
CREATE INDEX one_time_claims_expires_idx ON platform.one_time_claims (expires_at);
