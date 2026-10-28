-- Privileges applied by the migration job after migrations (ADR-0006).
-- Roles themselves are created by the database operator (CloudNativePG managed roles):
--   birge_migrator: owns schemas and tables, runs DDL.
--   birge_app:      the application; no DDL.

GRANT USAGE ON SCHEMA platform TO birge_app;

GRANT SELECT, INSERT, UPDATE, DELETE ON platform.outbox, platform.processed_events, platform.timers TO birge_app;

-- Data keys: no DELETE; shredding is an UPDATE that leaves a tombstone (ADR-0009).
GRANT SELECT, INSERT, UPDATE ON platform.member_keys TO birge_app;
REVOKE DELETE, TRUNCATE ON platform.member_keys FROM birge_app;

-- Append-only.
GRANT SELECT, INSERT ON platform.audit_log TO birge_app;
REVOKE UPDATE, DELETE, TRUNCATE ON platform.audit_log FROM birge_app;

-- Staff accounts: kept as the names behind audit actor IDs; no DELETE.
GRANT SELECT, INSERT, UPDATE ON platform.staff_accounts TO birge_app;
REVOKE DELETE, TRUNCATE ON platform.staff_accounts FROM birge_app;

-- Context schemas: the application reads and writes its own rows, no DDL.
-- DELETE is needed by account deletion and retention (AGENTS.md N6); they
-- are the only code paths that delete. "ALL TABLES" is re-evaluated on every
-- migrate run, so a new table in an existing schema is covered; a new schema
-- must be added here (wiring TestRolesCoverEverySchema fails otherwise).
GRANT USAGE ON SCHEMA membership TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA membership TO birge_app;
GRANT USAGE ON SCHEMA notify TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA notify TO birge_app;
GRANT USAGE ON SCHEMA privacy TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA privacy TO birge_app;
GRANT USAGE ON SCHEMA safety TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA safety TO birge_app;
GRANT USAGE ON SCHEMA wellbeing TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA wellbeing TO birge_app;
GRANT USAGE ON SCHEMA emergency TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA emergency TO birge_app;
GRANT USAGE ON SCHEMA assistant TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA assistant TO birge_app;
GRANT USAGE ON SCHEMA knowledge TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA knowledge TO birge_app;
GRANT USAGE ON SCHEMA circle TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA circle TO birge_app;
GRANT USAGE ON SCHEMA chat TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA chat TO birge_app;
GRANT USAGE ON SCHEMA sharing TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA sharing TO birge_app;
GRANT USAGE ON SCHEMA directory TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA directory TO birge_app;
GRANT USAGE ON SCHEMA moderation TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA moderation TO birge_app;
