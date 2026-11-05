#!/bin/sh
set -eu

# A unique Compose project owns every container and volume removed below.
project="cycrio-pg16-$(date +%s)-$$"
compose_file=deploy/test/postgres16.compose.yaml
compose() {
  docker compose -p "$project" -f "$compose_file" "$@"
}
cleanup() {
  compose down --volumes --remove-orphans >/dev/null
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

compose up -d --wait postgres
compose exec -T postgres psql -X -U postgres -d postgres -f /dev/stdin < deploy/production/init-database.sql
# Only this synthetic, non-published DB uses trust for the runtime roles.
compose exec -T postgres sh -c 'printf "local all all trust\nhost all all all trust\n" > "$PGDATA/pg_hba.conf"'
compose exec -T postgres psql -X -U postgres -d postgres -c 'SELECT pg_reload_conf()'
compose run --rm --no-deps tests go run ./cmd/manage migrate
compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U cycrio -d cycrio <<'SQL'
BEGIN;
INSERT INTO users (id,state) VALUES ('00000000-0000-4000-8000-000000000001','active');
UPDATE users SET state='disabled' WHERE id='00000000-0000-4000-8000-000000000001';
DELETE FROM users WHERE id='00000000-0000-4000-8000-000000000001';
DO $$ BEGIN
  IF has_schema_privilege(current_user, 'cycrio', 'CREATE') THEN
    RAISE EXCEPTION 'runtime role must not have DDL privileges';
  END IF;
END $$;
ROLLBACK;
SQL
compose run --rm --no-deps tests
