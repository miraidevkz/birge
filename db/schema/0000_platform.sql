-- Desired schema of the platform tables (declarative). The full desired schema is
-- every file in db/schema/ in name order; one file per bounded context
-- (NNNN_<context>.sql). See ADR-0006 and the db-migration skill. PostgreSQL 18.

CREATE SCHEMA IF NOT EXISTS platform;

-- Transactional outbox (ADR-0007): written in the same transaction as the domain change.
CREATE TABLE platform.outbox (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    event_type    text        NOT NULL CHECK (event_type ~ '^[a-z]+\.[A-Z][A-Za-z]+$'),
    event_version integer     NOT NULL CHECK (event_version >= 1),
    payload       jsonb       NOT NULL,
    trace_parent  text,
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    published_at  timestamptz
);

CREATE INDEX outbox_unpublished_idx ON platform.outbox (occurred_at) WHERE published_at IS NULL;

-- Consumer-side deduplication: an event is handled at most once per consumer.
CREATE TABLE platform.processed_events (
    consumer     text        NOT NULL,
    event_id     uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- Durable timers (ADR-0020): created in the same transaction as the domain change.
CREATE TABLE platform.timers (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    kind         text        NOT NULL,
    subject_id   uuid        NOT NULL,
    dedup_key    text,
    due_at       timestamptz NOT NULL,
    payload      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    attempts     integer     NOT NULL DEFAULT 0,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    done_at      timestamptz
);

CREATE INDEX timers_due_idx ON platform.timers (due_at) WHERE done_at IS NULL;
CREATE UNIQUE INDEX timers_active_dedup_idx ON platform.timers (kind, dedup_key) WHERE done_at IS NULL AND dedup_key IS NOT NULL;

-- Audit log: append-only (the application role has INSERT and SELECT only; see db/roles.sql).
CREATE TABLE platform.audit_log (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    actor_id    uuid        NOT NULL,
    actor_role  text        NOT NULL,
    action      text        NOT NULL,
    object_type text        NOT NULL,
    object_id   uuid,
    reason      text
);

CREATE INDEX audit_log_object_idx ON platform.audit_log (object_type, object_id, occurred_at);
CREATE INDEX audit_log_actor_idx ON platform.audit_log (actor_id, occurred_at);
-- Keyset paging of the staff audit log, newest first (package staffdir).
CREATE INDEX audit_log_occurred_idx ON platform.audit_log (occurred_at DESC, id DESC);

-- Staff accounts (ADR-0008 staff addendum, package staffdir): one row per staff
-- member whose token has reached the API, upserted by the staff auth middleware
-- at most hourly. The IdP stays the authority on who is staff; these rows are
-- the names behind the staff IDs in audit_log, so they are kept (no DELETE for
-- the application role, see db/roles.sql). display_name comes from the IdP
-- name or preferred_username claim, never the email.
CREATE TABLE platform.staff_accounts (
    staff_id      uuid        PRIMARY KEY,
    display_name  text        CHECK (char_length(display_name) BETWEEN 1 AND 80),
    roles         text[]      NOT NULL DEFAULT '{}'
        CHECK (roles <@ ARRAY['psychologist', 'moderator', 'lawyer', 'admin']::text[]),
    first_seen_at timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL
);

-- Per-member data keys (ADR-0009): a Tink AEAD keyset per member, wrapped by the
-- key-encryption key named in kek_id. Crypto-shredding clears wrapped_dek and sets
-- destroyed_at; the row stays as a tombstone so the member's key is never
-- re-created and later writes are refused. Rows are never deleted.
CREATE TABLE platform.member_keys (
    member_id    uuid        PRIMARY KEY,
    wrapped_dek  bytea       NOT NULL,
    kek_id       text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    destroyed_at timestamptz,
    CONSTRAINT member_keys_tombstone_check
        CHECK ((destroyed_at IS NULL) = (octet_length(wrapped_dek) > 0))
);

-- Fixed-window rate limit counters shared by every replica (package
-- ratelimit, PostgresStore): the API, plan link and auth gateway limits and
-- the worker's SMS guards. key is an HMAC of the limiter name and its key
-- (BIRGE_HASH_KEY_FILE), never an IP or a phone number. One row per key and
-- window, so a hit is one INSERT ... ON CONFLICT DO UPDATE; rows past
-- expires_at are never read again and the worker's sweeper deletes them. A
-- regular (logged) table, not UNLOGGED: the SMS daily budget lives here and
-- must survive a crash or a failover (internal/platform/ratelimit/README.md).
CREATE TABLE platform.rate_counters (
    key          text        NOT NULL,
    window_start timestamptz NOT NULL,
    hits         bigint      NOT NULL CHECK (hits >= 1),
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (key, window_start)
);

CREATE INDEX rate_counters_expires_idx ON platform.rate_counters (expires_at);

-- One-time claims shared by every replica (package ratelimit): a proof-of-work
-- solution of the auth gateway is accepted once. key is an HMAC like above.
-- Logged on purpose: losing it in a crash or a failover would let solutions
-- issued shortly before be used again. The worker's sweeper deletes rows
-- past expires_at.
CREATE TABLE platform.one_time_claims (
    key        text        PRIMARY KEY,
    expires_at timestamptz NOT NULL
);

CREATE INDEX one_time_claims_expires_idx ON platform.one_time_claims (expires_at);
