# ADR-0006: PostgreSQL, declarative schema, safe migrations

Status: Accepted · Date: 2026-10-27

## Context
No dedicated DBA. Data loss is the worst failure. Rolling updates on Kubernetes run old and new versions against one database.

## Decision
- PostgreSQL, operated by CloudNativePG (replicas, continuous WAL archiving, point-in-time recovery).
- `db/schema.sql` is the desired schema. `psqldef --dry-run` produces the diff; it is saved as a versioned `goose` migration and reviewed. Queries are plain SQL compiled by `sqlc`.
- Guards: no destructive operations (DROP, TRUNCATE, narrowing types) without an explicit approval marker and an ADR reference; migration linter; mandatory `lock_timeout`/`statement_timeout`; expand/contract only (removal ships in a later release); snapshot before every migration; migrations run in a transaction; the application role has no DDL rights; audit, events and closed escalations are append-only.
- Renames are written by hand (a diff sees them as drop + add).

## Alternatives
Atlas: open-core with paid features; rejected by ADR-0012.

## Consequences
Restore drills run on a schedule; a backup that was never restored does not count. To verify: psqldef behaviour for dropped columns without the drop flag.
