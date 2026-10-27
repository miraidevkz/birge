-- Development only: the roles db/roles.sql expects, and one database each for
-- birge, Kratos and Hydra (staff sign-in). Authentication is "trust" inside the Compose network and
-- PostgreSQL publishes no port, so there is no password to keep.
CREATE ROLE birge_migrator LOGIN;
CREATE ROLE birge_app LOGIN;
CREATE ROLE kratos LOGIN;
CREATE ROLE hydra LOGIN;
CREATE DATABASE birge OWNER birge_migrator;
CREATE DATABASE kratos OWNER kratos;
CREATE DATABASE hydra OWNER hydra;
