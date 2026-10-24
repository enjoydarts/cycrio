-- Local-only credentials. This file is never used by production deployments.
CREATE ROLE cycrio_migrator LOGIN PASSWORD 'cycrio-migrator-local';
ALTER DATABASE cycrio OWNER TO cycrio_migrator;
CREATE ROLE cycrio LOGIN PASSWORD 'cycrio-local';
REVOKE ALL ON DATABASE cycrio FROM PUBLIC;
GRANT CONNECT ON DATABASE cycrio TO cycrio;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA cycrio AUTHORIZATION cycrio_migrator;
GRANT USAGE ON SCHEMA cycrio TO cycrio;
ALTER ROLE cycrio SET search_path TO cycrio, public;
ALTER ROLE cycrio_migrator SET search_path TO cycrio, public;
ALTER DEFAULT PRIVILEGES FOR ROLE cycrio_migrator IN SCHEMA cycrio
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO cycrio;
ALTER DEFAULT PRIVILEGES FOR ROLE cycrio_migrator IN SCHEMA cycrio
  GRANT USAGE, SELECT ON SEQUENCES TO cycrio;
