-- One-time setup using a PostgreSQL administrator. No existing DB is changed.
-- Set passwords afterward with psql's \password; never put them in this file.
\set ON_ERROR_STOP on

BEGIN;
CREATE ROLE cycrio_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE ROLE cycrio LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
COMMIT;

CREATE DATABASE cycrio OWNER cycrio_migrator;
REVOKE ALL ON DATABASE cycrio FROM PUBLIC;
GRANT CONNECT ON DATABASE cycrio TO cycrio;

\connect cycrio
BEGIN;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA cycrio AUTHORIZATION cycrio_migrator;
GRANT USAGE ON SCHEMA cycrio TO cycrio;
ALTER ROLE cycrio IN DATABASE cycrio SET search_path TO cycrio, public;
ALTER ROLE cycrio_migrator IN DATABASE cycrio SET search_path TO cycrio, public;
ALTER DEFAULT PRIVILEGES FOR ROLE cycrio_migrator IN SCHEMA cycrio
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO cycrio;
ALTER DEFAULT PRIVILEGES FOR ROLE cycrio_migrator IN SCHEMA cycrio
  GRANT USAGE, SELECT ON SEQUENCES TO cycrio;
COMMIT;
