-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
CREATE SCHEMA IF NOT EXISTS knowledge;
CREATE TABLE knowledge.topics (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    kind       text        NOT NULL
                           CHECK (kind IN ('rights', 'selfhelp')),
    code       text        NOT NULL
                           CHECK (code ~ '^[a-z_]{2,40}$'),
    title_kk   text        CHECK (title_kk IS NULL OR char_length(title_kk) BETWEEN 1 AND 120),
    title_ru   text        CHECK (title_ru IS NULL OR char_length(title_ru) BETWEEN 1 AND 120),
    title_en   text        CHECK (title_en IS NULL OR char_length(title_en) BETWEEN 1 AND 120),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT topics_code_key UNIQUE (code)
);
CREATE TABLE knowledge.articles (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    topic_id   uuid        NOT NULL REFERENCES knowledge.topics (id),
    kind       text        NOT NULL
                           CHECK (kind IN ('rights', 'selfhelp')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE knowledge.versions (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    article_id      uuid        NOT NULL REFERENCES knowledge.articles (id),
    lang            text        NOT NULL
                                CHECK (lang IN ('kk', 'ru', 'en')),
    status          text        NOT NULL
                                CHECK (status IN ('draft', 'in_review', 'published', 'retired')),
    title           text        NOT NULL
                                CHECK (char_length(title) BETWEEN 1 AND 200),
    body            text        NOT NULL
                                CHECK (char_length(body) BETWEEN 1 AND 20000),
    source_url      text        CHECK (source_url IS NULL OR char_length(source_url) BETWEEN 1 AND 2048),
    author_id       uuid        NOT NULL,
    reviewer_id     uuid,
    reviewed_at     timestamptz,
    expires_at      timestamptz,
    reject_reason   text        CHECK (reject_reason IS NULL OR reject_reason IN (
                                    'inaccurate', 'outdated_law', 'unclear',
                                    'unsafe_advice', 'missing_source', 'other')),
    needs_sync      boolean     NOT NULL DEFAULT false,
    idempotency_key uuid,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    CONSTRAINT versions_author_idempotency_key UNIQUE (author_id, idempotency_key),
    CONSTRAINT versions_published_fields_check CHECK (
        (status <> 'published'
            AND reviewer_id IS NULL AND reviewed_at IS NULL AND expires_at IS NULL)
        OR (status = 'published'
            AND reviewer_id IS NOT NULL AND reviewed_at IS NOT NULL AND expires_at IS NOT NULL)
    ),
    CONSTRAINT versions_reject_only_on_draft CHECK (
        reject_reason IS NULL OR status = 'draft'
    )
);
CREATE INDEX articles_topic_idx ON knowledge.articles (topic_id);
CREATE UNIQUE INDEX versions_one_published_per_article_lang
    ON knowledge.versions (article_id, lang)
    WHERE status = 'published';
CREATE UNIQUE INDEX versions_one_open_per_article_lang
    ON knowledge.versions (article_id, lang)
    WHERE status IN ('draft', 'in_review');
CREATE INDEX versions_article_lang_idx
    ON knowledge.versions (article_id, lang, status);
CREATE INDEX versions_status_updated_idx
    ON knowledge.versions (status, updated_at DESC, id DESC);
