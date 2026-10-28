#!/bin/sh
set -eu
umask 077

mkdir -p .local/evaluation
directory="$(mktemp -d .local/evaluation/hatchet-XXXXXX)"
started="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
check_services() {
  for service in api worker hatchet; do
    service_ids="$(docker compose ps --status running -q "$service")"
    # Compose can return success for an absent service. Check each separately.
    set -- $service_ids
    if [ "$#" -ne 1 ]; then
      echo 'The local api, worker and hatchet services must all be running.' >&2
      exit 1
    fi
  done
}
check_services
printf 'Synthetic Hatchet evaluation artifacts: %s\n' "$directory"
if ! docker compose run --rm --no-deps -T \
  -e 'DATABASE_URL=postgres://cycrio_migrator:cycrio-migrator-local@postgres:5432/cycrio?sslmode=disable' \
  api sh /app/deploy/local/test-hatchet-evaluation.sh "/app/$directory" \
  > "$directory/evaluation-worker.log" 2>&1; then
  echo 'Synthetic Hatchet evaluation failed; inspect the private artifact directory.' >&2
  exit 1
fi

check_services
: > "$directory/docker-services.log"
for service in api worker hatchet; do
  docker compose logs --no-color --since "$started" "$service" >> "$directory/docker-services.log" 2>&1
done
check_services
for scope in evaluation_worker docker_services; do
  case "$scope" in
    evaluation_worker) input="$directory/evaluation-worker.log"; set -- ;;
    docker_services) input="$directory/docker-services.log"; set -- -allow-empty ;;
  esac
  docker compose run --rm --no-deps -T api go run ./cmd/evalscan \
    -probes "/app/$directory/probes.json" -scope "$scope" "$@" \
    < "$input" > "$directory/$scope-check.json"
done
printf '{"version":1,"mode":"synthetic","scope":"local_hatchet_and_captured_logs","passed":true}\n' > "$directory/complete.json"
echo 'Synthetic Hatchet dispatch, history and captured log checks: passed'
