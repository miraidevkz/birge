-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
ALTER TABLE "emergency"."sos_requests" ADD COLUMN "seen_at" timestamp WITH TIME ZONE;
ALTER TABLE "emergency"."sos_requests" ADD COLUMN "seen_by" uuid;
ALTER TABLE "emergency"."sos_requests" ADD COLUMN "closed_by" uuid;
ALTER TABLE "emergency"."sos_requests" ADD CONSTRAINT "sos_requests_seen_check" CHECK ((seen_at is null) = (seen_by is null));
ALTER TABLE "emergency"."sos_requests" ADD CONSTRAINT "sos_requests_closed_by_check" CHECK (closed_by is null or status = 'closed' and close_reason = 'author');
CREATE INDEX sos_requests_staff_queue_idx
    ON emergency.sos_requests (status, created_at, id);
