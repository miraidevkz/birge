-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
ALTER TABLE "knowledge"."articles" ADD COLUMN "slug" text NOT NULL CHECK (slug ~ '^[a-z0-9_]{2,60}$');
ALTER TABLE "knowledge"."articles" ADD CONSTRAINT "articles_topic_slug_key" UNIQUE ("topic_id", "slug");
ALTER TABLE "knowledge"."versions" ADD COLUMN "review_due_at" timestamp WITH TIME ZONE;
CREATE INDEX versions_review_due_expires_idx
    ON knowledge.versions (expires_at ASC, id ASC)
    WHERE status = 'published' AND review_due_at IS NOT NULL;
