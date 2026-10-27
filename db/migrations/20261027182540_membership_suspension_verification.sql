-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
ALTER TABLE "membership"."members" ADD COLUMN "suspended_at" timestamp WITH TIME ZONE;
ALTER TABLE "membership"."members" ADD COLUMN "suspended_reason" text CHECK (suspended_reason is null or suspended_reason ~ '^[a-z][a-z0-9_]{0,39}$');
ALTER TABLE "membership"."members" ADD CHECK ((suspended_at is null) = (suspended_reason is null));
ALTER TABLE "membership"."verification_requests" ADD COLUMN "reject_reason" text CHECK (reject_reason is null or reject_reason ~ '^[a-z][a-z0-9_]{0,39}$');
