-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE TABLE analytics.erased_subjects (
    subject   bytea       PRIMARY KEY CHECK (octet_length(subject) = 32),
    erased_at timestamptz NOT NULL
);
