-- Desired schema of the privacy bounded context (declarative). See ADR-0006.

CREATE SCHEMA IF NOT EXISTS privacy;

-- Append-only consent history. Current state = latest row per (member_id, type).
CREATE TABLE privacy.consent_history (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    member_id  uuid        NOT NULL,
    type       text        NOT NULL
                           CHECK (type IN ('data_processing', 'ai_crossborder', 'research')),
    version    text        NOT NULL
                           CHECK (char_length(version) BETWEEN 1 AND 32),
    granted    boolean     NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX consent_history_member_type_idx
    ON privacy.consent_history (member_id, type, changed_at DESC, id DESC);

-- One deletion request per member. completed_at set when crypto-shred runs.
CREATE TABLE privacy.deletion_requests (
    member_id    uuid        PRIMARY KEY,
    requested_at timestamptz NOT NULL,
    due_at       timestamptz NOT NULL,
    completed_at timestamptz
);
