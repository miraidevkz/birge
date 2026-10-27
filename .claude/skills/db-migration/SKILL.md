---
name: db-migration
description: Change the database schema safely in Birge. Use for any change to tables, columns, indexes or constraints, or when writing or reviewing a migration in db/.
---

# Database migration

Data loss is the worst failure in this project. Follow these steps exactly; see ADR-0006.

## Steps

1. Edit `db/schema.sql` (the desired state). Never edit an existing file in `db/migrations/`.
2. Generate: `make migration name=<slug>`. It runs `psqldef --dry-run` against an ephemeral PostgreSQL and saves the diff as a new goose migration.
3. Read the generated SQL line by line. It must contain only additive or compatible changes.
4. Make sure the migration starts with:
   ```sql
   SET lock_timeout = '5s';
   SET statement_timeout = '60s';
   ```
5. Update queries in `db/queries/` and run `make sqlc`.
6. Run `make db-check`: migration lint, destructive-operation gate, apply on a fresh database, sqlc diff.

## Forbidden without an approval marker and an ADR reference

`DROP TABLE`, `DROP COLUMN`, `TRUNCATE`, `DELETE` without `WHERE`, narrowing a type, adding `NOT NULL` without a default to a populated table, dropping a constraint that protects data.

## Expand/contract

Old and new application versions run against the same database during a rolling update.
- Release N: add the new column/table, write to both, backfill.
- Release N+1: read from the new one.
- Release N+2: remove the old one (destructive, needs approval).

## Renames

A diff sees a rename as drop + add, which loses data. Write renames by hand as expand/contract and ask for human review.

## Ownership

A table belongs to one bounded context. Never add a foreign key or a query that crosses contexts; use IDs and events.
