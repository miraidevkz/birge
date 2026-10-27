-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS moderation;
CREATE TABLE moderation.items (
    id            uuid        PRIMARY KEY,
    kind          text        NOT NULL
                              CHECK (kind IN ('listing', 'specialist', 'review', 'report', 'chat_message', 'verification')),
    object_id     uuid        NOT NULL,
    status        text        NOT NULL DEFAULT 'open'
                              CHECK (status IN ('open', 'decided')),
    source_reason text        CHECK (source_reason IS NULL OR source_reason ~ '^[a-z][a-z0-9_]{0,39}$'),
    report_count  integer     NOT NULL DEFAULT 1 CHECK (report_count >= 1),
    created_at    timestamptz NOT NULL,
    decided_by    uuid,
    decided_at    timestamptz,
    approve       boolean,
    reason        text        CHECK (reason IS NULL OR reason ~ '^[a-z][a-z_]{0,39}$'),
    block_author  boolean     NOT NULL DEFAULT false,
    legal_hold    boolean     NOT NULL DEFAULT false,
    CONSTRAINT items_decision_check CHECK (
        (status = 'open' AND decided_by IS NULL AND decided_at IS NULL AND approve IS NULL
            AND reason IS NULL AND NOT block_author AND NOT legal_hold)
        OR (status = 'decided' AND decided_by IS NOT NULL AND decided_at IS NOT NULL AND approve IS NOT NULL)
    ),
    CONSTRAINT items_reject_reason_check CHECK (approve IS DISTINCT FROM false OR reason IS NOT NULL),
    CONSTRAINT items_flags_kind_check CHECK (
        kind IN ('report', 'chat_message') OR (NOT block_author AND NOT legal_hold)
    )
);
CREATE UNIQUE INDEX items_open_object_uidx
    ON moderation.items (kind, object_id)
    WHERE status = 'open';
CREATE INDEX items_open_queue_idx
    ON moderation.items (created_at, id)
    WHERE status = 'open';
CREATE INDEX items_open_kind_queue_idx
    ON moderation.items (kind, created_at, id)
    WHERE status = 'open';
