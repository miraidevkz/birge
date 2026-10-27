---
name: supabase-postgres-best-practices
description: "Postgres performance and schema reference (indexes, query plans, connections, locking, constraints, data types). Use together with the db-migration skill, which takes precedence for schema changes."
license: MIT
metadata:
  source: https://github.com/supabase/agent-skills
  commit: c9be0e931b7930f7d02126d04774d904c381e7d7
  vendored: "2026-10-27"
---

> **Birge overrides** (take precedence over anything below; see AGENTS.md and docs/adr/):
> - Ignore Supabase-specific parts (`auth.uid()`, Supabase dashboard/docs). Row-level security is decided by an ADR.
> - No `ALTER SYSTEM`: PostgreSQL configuration lives in CloudNativePG/Helm; pooling via the CloudNativePG Pooler, not pgbouncer.ini.
> - The db-migration skill and ADR-0006 win over this skill: no DROP (including old partitions) without an approval marker and an ADR; add foreign keys as NOT VALID, then VALIDATE, with `lock_timeout`.
> - Use built-in `uuidv7()` on PostgreSQL 18; an extension is a new dependency.

# Supabase Postgres Best Practices

Comprehensive performance optimization guide for Postgres, maintained by Supabase. Contains rules across 8 categories, prioritized by impact to guide automated query optimization and schema design.

## When to Apply

Reference these guidelines when:
- Writing SQL queries or designing schemas
- Implementing indexes or query optimization
- Reviewing database performance issues
- Configuring connection pooling or scaling
- Optimizing for Postgres-specific features
- Working with Row-Level Security (RLS)

## Rule Categories by Priority

| Priority | Category | Impact | Prefix |
|----------|----------|--------|--------|
| 1 | Query Performance | CRITICAL | `query-` |
| 2 | Connection Management | CRITICAL | `conn-` |
| 3 | Security & RLS | CRITICAL | `security-` |
| 4 | Schema Design | HIGH | `schema-` |
| 5 | Concurrency & Locking | MEDIUM-HIGH | `lock-` |
| 6 | Data Access Patterns | MEDIUM | `data-` |
| 7 | Monitoring & Diagnostics | LOW-MEDIUM | `monitor-` |
| 8 | Advanced Features | LOW | `advanced-` |

## How to Use

Read individual rule files for detailed explanations and SQL examples:

```
references/query-missing-indexes.md
references/query-partial-indexes.md
references/_sections.md
```

Each rule file contains:
- Brief explanation of why it matters
- Incorrect SQL example with explanation
- Correct SQL example with explanation
- Optional EXPLAIN output or metrics
- Additional context and references
- Supabase-specific notes (when applicable)

## References

- https://www.postgresql.org/docs/current/
- https://supabase.com/docs
- https://wiki.postgresql.org/wiki/Performance_Optimization
- https://supabase.com/docs/guides/database/overview
- https://supabase.com/docs/guides/auth/row-level-security
