-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
ALTER TABLE "membership"."members" ADD COLUMN "deleted_at" timestamp WITH TIME ZONE;
