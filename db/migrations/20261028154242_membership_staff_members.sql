-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
ALTER TABLE "membership"."members" ADD COLUMN "verified_at" timestamp WITH TIME ZONE;
-- Backfill (hand-written): members verified before this column existed get
-- their last update time, so the staff list's keyset order never meets a
-- NULL. Not destructive: only fills the new column.
UPDATE membership.members SET verified_at = updated_at
WHERE verification = 'verified' AND verified_at IS NULL AND deleted_at IS NULL;
CREATE INDEX members_staff_verified_idx ON membership.members (verified_at DESC, id DESC)
    WHERE verification = 'verified' AND deleted_at IS NULL;
CREATE INDEX members_staff_verified_district_idx ON membership.members (district_id, verified_at DESC, id DESC)
    WHERE verification = 'verified' AND deleted_at IS NULL;
CREATE INDEX members_staff_suspended_idx ON membership.members (suspended_at DESC, id DESC)
    WHERE suspended_at IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX members_staff_suspended_district_idx ON membership.members (district_id, suspended_at DESC, id DESC)
    WHERE suspended_at IS NOT NULL AND deleted_at IS NULL;
