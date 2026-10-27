---
name: db-migration
description: Change the database schema safely in Birge. Use for any change to tables, columns, indexes or constraints, or when writing or reviewing a migration in db/.
---

# Database migration

Data loss is the worst failure in this project. Follow these steps exactly; see ADR-0006.

## Steps

1. Edit `db/schema/<NNN_context>.sql` (the desired state). Never edit an existing file in `db/migrations/`.
2. Generate: `make -s migration name=<snake_case>`. It applies the existing migrations to a throwaway PostgreSQL, runs `psqldef --dry-run` against `db/schema/<NNN_context>.sql`, checks that the result applies and matches, and **prints** the migration with its file name on the first line (`-- file: db/migrations/<timestamp>_<name>.sql`). Save the rest under that name. It refuses to generate destructive statements.
3. Read the generated SQL line by line. It must contain only additive or compatible changes.
4. The generated migration already starts with (hand-written ones must too):
   ```sql
   SET lock_timeout = '5s';
   SET statement_timeout = '60s';
   ```
5. Update queries in `db/queries/` and run `make sqlc`.
6. Run `make db-check`: destructive-operation gate and `lock_timeout` check on every file, all migrations applied to a fresh database, result compared with `db/schema/<NNN_context>.sql`. `make go-check` includes it and `sqlc-check`.

## Forbidden without the marker `-- birge:destructive-approved ADR-NNNN`

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
