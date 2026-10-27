-- Desired schema of the moderation bounded context (declarative).
-- See docs/requirements/mvp.md section 6, ADR-0016 and ADR-0019.
-- The staff queue: one item per object under review. Moderation owns no
-- content. Summaries are fetched from the owning context when the queue is
-- read and are never stored here. Rows are never deleted: an item moves from
-- open to decided and stays as the record of the decision.

CREATE SCHEMA IF NOT EXISTS moderation;

-- One queue item. kind and object_id name the owner and the object
-- (listing, specialist, review, chat message for kinds report and
-- chat_message, verification request). source_reason is the short code the
-- owner sent with the event (hold reason or report reason), never prose.
-- report_count counts reports folded into an open report item.
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

-- At most one open item per object: the natural key of idempotent creation.
-- A decided object submitted again (a resubmitted review) gets a new item.
CREATE UNIQUE INDEX items_open_object_uidx
    ON moderation.items (kind, object_id)
    WHERE status = 'open';
-- The queue: open items oldest first, all kinds or one kind.
CREATE INDEX items_open_queue_idx
    ON moderation.items (created_at, id)
    WHERE status = 'open';
CREATE INDEX items_open_kind_queue_idx
    ON moderation.items (kind, created_at, id)
    WHERE status = 'open';
