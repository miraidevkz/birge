-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';
ALTER TABLE "membership"."members" ADD COLUMN "last_active_day" date;
