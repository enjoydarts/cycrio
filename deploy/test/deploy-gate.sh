#!/bin/sh
# Exercise the real production dependency graph with synthetic commands and no DB.
set -eu
task_dir=$(mktemp -d)
project="cycrio-deploy-gate-$$"
export CYCRIO_SECRETS_DIR="$task_dir"
for secret in database_url migration_database_url hatchet_token bootstrap.json keyring.json; do
  printf 'synthetic\n' > "$task_dir/$secret"
done
cat > "$task_dir/override.yaml" <<'YAML'
services:
  migrate:
    image: alpine:3.24.3
    entrypoint: [/bin/sh, -ec]
    command: ['exit $${MIGRATION_EXIT}']
    environment:
      MIGRATION_EXIT: ${MIGRATION_EXIT:-1}
  api:
    image: alpine:3.24.3
    entrypoint: [/bin/sh, -ec]
    command: ['exec sleep 120']
    healthcheck:
      disable: false
      test: [CMD, 'true']
      interval: 1s
      start_period: 0s
  worker:
    image: alpine:3.24.3
    entrypoint: [/bin/sh, -ec]
    command: ['exec sleep 120']
    environment:
      WORKER_HEALTH: ${WORKER_HEALTH:-healthy}
    healthcheck:
      disable: false
      test: [CMD-SHELL, 'test "$${WORKER_HEALTH}" = healthy']
      interval: 1s
      retries: 1
      start_period: 0s
YAML
compose() {
  docker compose -p "$project" -f compose.coolify.yaml -f "$task_dir/override.yaml" "$@"
}
cleanup() {
  compose down --volumes >/dev/null 2>&1 || true
  rm -rf "$task_dir"
}
trap cleanup EXIT INT TERM
export MIGRATION_EXIT=1
if compose up -d --wait --wait-timeout 10 --no-build > "$task_dir/failure.log" 2>&1; then
  echo 'Expected failed migration to block deployment' >&2
  exit 1
fi
for service in api worker; do
  if [ -n "$(compose ps --status running -q "$service")" ]; then
    echo "$service started despite migration failure" >&2
    exit 1
  fi
done
compose down --volumes >/dev/null 2>&1
export MIGRATION_EXIT=0
export WORKER_HEALTH=unhealthy
if compose up -d --wait --wait-timeout 10 --no-build > "$task_dir/health.log" 2>&1; then
  echo 'Expected unhealthy Worker to fail deployment' >&2
  exit 1
fi
compose down --volumes >/dev/null 2>&1
export WORKER_HEALTH=healthy
compose up -d --wait --wait-timeout 10 --no-build >/dev/null
for service in api worker; do
  test -n "$(compose ps --status running -q "$service")"
done
test "$(docker inspect --format '{{.State.ExitCode}}' "$(compose ps -a -q migrate)")" = 0
echo 'Migration failure blocks API/Worker; unhealthy Worker blocks completion; healthy release succeeds.'
