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

GRANT USAGE ON SCHEMA notify TO birge_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA notify TO birge_app;
