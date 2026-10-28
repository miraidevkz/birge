-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
ALTER TABLE "membership"."cities" ADD COLUMN "active" boolean NOT NULL DEFAULT true;
ALTER TABLE "membership"."districts" ADD COLUMN "active" boolean NOT NULL DEFAULT true;
ALTER TABLE "membership"."needs" ADD COLUMN "active" boolean NOT NULL DEFAULT true;
