-- Desired schema of the assistant bounded context (declarative). See ADR-0006.

CREATE SCHEMA IF NOT EXISTS assistant;

-- AI assistant dialogue turns (docs/requirements/mvp.md § 5 step 8, § 7 ai_messages).
-- Only anonymized text is kept, sealed with the member's data key (ADR-0009),
-- and rows older than the retention period (90 days) are purged daily by the
-- assistant.RetentionPurge timer (ADR-0020).
CREATE TABLE assistant.messages (
    id              uuid        PRIMARY KEY,
    member_id       uuid        NOT NULL,
    role            text        NOT NULL CHECK (role IN ('member', 'assistant')),
    -- Assistant turns point at the member turn they answer.
    reply_to        uuid,
    -- Personal data: anonymized text sealed with associated data
    -- assistant.messages.text|<id>. NULL for crisis and degraded assistant turns.
    text_sealed     bytea       CHECK (octet_length(text_sealed) <= 16384),
    crisis_flag     boolean     NOT NULL DEFAULT false,
    degraded        boolean     NOT NULL DEFAULT false,
    -- Knowledge-base references of an assistant turn: [{article_id, title, source_url}].
    sources         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    suggest         text        NOT NULL DEFAULT 'none'
                                CHECK (suggest IN ('none', 'tea', 'psychologist', 'rights', 'practice')),
    escalation_id   uuid,
    -- Member turns only: the Idempotency-Key of the request.
    idempotency_key uuid,
    helped          boolean,
    rated_at        timestamptz,
    created_at      timestamptz NOT NULL,
    CHECK ((role = 'member') = (idempotency_key IS NOT NULL)),
    CHECK ((role = 'assistant') = (reply_to IS NOT NULL))
);

CREATE UNIQUE INDEX messages_member_idempotency_uidx
    ON assistant.messages (member_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX messages_reply_to_uidx
    ON assistant.messages (reply_to) WHERE reply_to IS NOT NULL;
CREATE INDEX messages_member_created_idx
    ON assistant.messages (member_id, created_at DESC, id DESC);
CREATE INDEX messages_created_idx
    ON assistant.messages (created_at);
