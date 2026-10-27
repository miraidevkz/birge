-- Desired schema of the sharing bounded context (declarative). See ADR-0015,
-- ADR-0016 and ADR-0019. Item listings with optional photos (re-encoded,
-- metadata stripped), contact requests that open a private chat only with the
-- author's consent. Rows are never deleted: listings close, contact requests
-- change status; the only deletion path stays the account-deletion flow.

CREATE SCHEMA IF NOT EXISTS sharing;

-- One listing. Status pending until moderation approves and any photo has
-- been processed; then active until the author or the auto-close timer closes
-- it. photo_object_key is set only after ProcessPhoto succeeds (processed JPEG).
CREATE TABLE sharing.listings (
    id                uuid        PRIMARY KEY,
    author_id         uuid        NOT NULL,
    idempotency_key   uuid        NOT NULL,
    type              text        NOT NULL
                                  CHECK (type IN ('give', 'swap', 'need')),
    category          text        NOT NULL
                                  CHECK (category ~ '^[a-z_]{2,40}$'),
    title             text        NOT NULL
                                  CHECK (char_length(title) BETWEEN 3 AND 80),
    description       text        NOT NULL
                                  CHECK (char_length(description) BETWEEN 1 AND 1000),
    district_id       uuid        NOT NULL,
    photo_upload_id   uuid,
    -- Processed object key only (never the raw upload). NULL until ProcessPhoto.
    photo_object_key  text        CHECK (photo_object_key IS NULL OR char_length(photo_object_key) BETWEEN 1 AND 512),
    status            text        NOT NULL
                                  CHECK (status IN ('pending', 'active', 'rejected', 'closed')),
    reject_reason     text        CHECK (reject_reason IS NULL OR char_length(reject_reason) <= 40),
    -- Set when moderation.Decided approves; activation may wait for the photo.
    approved_at       timestamptz,
    closes_at         timestamptz,
    closed_at         timestamptz,
    created_at        timestamptz NOT NULL,
    CONSTRAINT listings_closes_check CHECK (closes_at IS NULL OR closes_at > created_at),
    CONSTRAINT listings_reject_check CHECK (
        (status = 'rejected' AND reject_reason IS NOT NULL)
        OR (status <> 'rejected' AND reject_reason IS NULL)
    ),
    CONSTRAINT listings_active_check CHECK (
        (status = 'active' AND approved_at IS NOT NULL AND closes_at IS NOT NULL)
        OR (status <> 'active')
    ),
    CONSTRAINT listings_closed_check CHECK (
        (status = 'closed' AND closed_at IS NOT NULL)
        OR (status <> 'closed' AND closed_at IS NULL)
    )
);

CREATE UNIQUE INDEX listings_author_idempotency_uidx
    ON sharing.listings (author_id, idempotency_key);
-- Feed: active, newest first; filters applied in the query.
CREATE INDEX listings_feed_idx
    ON sharing.listings (created_at DESC, id DESC)
    WHERE status = 'active';
CREATE INDEX listings_author_created_idx
    ON sharing.listings (author_id, created_at DESC, id DESC);
CREATE INDEX listings_feed_type_idx
    ON sharing.listings (type, created_at DESC, id DESC)
    WHERE status = 'active';
CREATE INDEX listings_feed_category_idx
    ON sharing.listings (category, created_at DESC, id DESC)
    WHERE status = 'active';
CREATE INDEX listings_feed_district_idx
    ON sharing.listings (district_id, created_at DESC, id DESC)
    WHERE status = 'active';

-- Presigned upload slots for item photos (ADR-0015). The client PUTs to
-- object_key; ProcessPhoto re-encodes, strips metadata and deletes the original.
CREATE TABLE sharing.photo_uploads (
    id                 uuid        PRIMARY KEY,
    member_id          uuid        NOT NULL,
    object_key         text        NOT NULL
                                   CHECK (char_length(object_key) BETWEEN 1 AND 512),
    content_type       text        NOT NULL
                                   CHECK (content_type IN ('image/jpeg', 'image/png')),
    size_bytes         integer     NOT NULL
                                   CHECK (size_bytes BETWEEN 1 AND 5242880),
    expires_at         timestamptz NOT NULL,
    -- pending → processed | failed after sharing.ProcessPhoto.
    status             text        NOT NULL
                                   CHECK (status IN ('pending', 'processed', 'failed')),
    processed_object_key text      CHECK (processed_object_key IS NULL OR char_length(processed_object_key) BETWEEN 1 AND 512),
    used_at            timestamptz,
    created_at         timestamptz NOT NULL,
    CONSTRAINT photo_uploads_processed_check CHECK (
        (status = 'processed' AND processed_object_key IS NOT NULL)
        OR (status <> 'processed' AND processed_object_key IS NULL)
    )
);

CREATE INDEX photo_uploads_member_created_idx
    ON sharing.photo_uploads (member_id, created_at DESC, id DESC);

ALTER TABLE sharing.listings
    ADD CONSTRAINT listings_photo_upload_fkey
    FOREIGN KEY (photo_upload_id) REFERENCES sharing.photo_uploads (id);

-- "Message the author": one request per listing and requester; contacts open
-- only when the author accepts (chat listens to sharing.ContactAccepted).
CREATE TABLE sharing.contact_requests (
    id           uuid        PRIMARY KEY,
    listing_id   uuid        NOT NULL REFERENCES sharing.listings (id),
    requester_id uuid        NOT NULL,
    status       text        NOT NULL
                             CHECK (status IN ('pending', 'accepted')),
    created_at   timestamptz NOT NULL,
    accepted_at  timestamptz,
    CONSTRAINT contact_requests_accepted_check CHECK (
        (status = 'accepted' AND accepted_at IS NOT NULL)
        OR (status = 'pending' AND accepted_at IS NULL)
    ),
    CONSTRAINT contact_requests_listing_requester_key UNIQUE (listing_id, requester_id)
);

CREATE INDEX contact_requests_listing_created_idx
    ON sharing.contact_requests (listing_id, created_at ASC, id ASC);
