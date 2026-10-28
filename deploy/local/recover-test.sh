#!/bin/sh
# Synthetic, disposable exercise. Never accepts a database URL or a real key path.
set -eu
umask 077
cd "$(dirname "$0")/../.."
mkdir -p .local/recovery
run_dir=$(mktemp -d .local/recovery/run.XXXXXXXX)
run_id=${run_dir##*/}
container_name=cycrio-recovery-$run_id
container_id=
log=$run_dir/exercise.log

cleanup() {
    status=$?
    trap - 0
    # The daemon may have started the container even when the CLI lost its output.
    # Check our unique label before removing a container found only by name.
    if [ -z "$container_id" ]; then
        candidate=$(docker inspect --format '{{.Id}} {{index .Config.Labels "io.cycrio.recovery.run"}}' "$container_name" 2>> "$log" || true)
        case "$candidate" in
            *" $run_id") container_id=${candidate% *} ;;
        esac
    fi
    if [ -n "$container_id" ]; then
        if ! docker rm -f "$container_id" >> "$log" 2>&1; then
            printf 'Recovery container cleanup failed: %s\n' "$container_name" >&2
            status=1
        fi
    fi
    if [ "$status" -ne 0 ]; then
        printf 'Recovery exercise failed; private log: %s\n' "$log" >&2
    fi
    exit "$status"
}
trap cleanup 0
trap 'exit 130' INT
trap 'exit 143' TERM HUP

container_id=$(docker compose run --rm -d --no-deps --name "$container_name" \
    --label "io.cycrio.recovery.run=$run_id" recovery-postgres 2>> "$log")
attempt=0
until docker exec "$container_id" pg_isready -U postgres -d postgres >> "$log" 2>&1; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 60 ]; then exit 1; fi
    sleep 1
done
docker exec -i "$container_id" psql -X -v ON_ERROR_STOP=1 -U postgres -d postgres >> "$log" 2>&1 <<'SQL'
CREATE ROLE cycrio_migrator LOGIN PASSWORD 'cycrio-migrator-local';
CREATE ROLE cycrio LOGIN PASSWORD 'cycrio-local';
ALTER ROLE cycrio_migrator SET search_path TO cycrio,public;
ALTER ROLE cycrio SET search_path TO cycrio,public;
CREATE DATABASE recovery_source OWNER cycrio_migrator TEMPLATE template0;
CREATE DATABASE recovery_restored OWNER cycrio_migrator TEMPLATE template0;
REVOKE ALL ON DATABASE recovery_source,recovery_restored FROM PUBLIC;
GRANT CONNECT ON DATABASE recovery_source,recovery_restored TO cycrio;
SQL

exercise() {
    docker compose run --rm -T --no-deps api go test -tags=integration,recovery ./internal/database \
        -run '^TestRecoveryExercise$' -count=1 -timeout=90s \
        -args -recovery-run "$run_id" -recovery-phase "$1"
}
if ! exercise seed >> "$log" 2>&1; then exit 1; fi
mkdir "$run_dir/db" "$run_dir/keys"
docker exec "$container_id" pg_dump -U postgres -d recovery_source -Fc > "$run_dir/db/cycrio.dump" 2>> "$log"
cp "$run_dir/source-keyring.json" "$run_dir/keys/keyring.json"
rm "$run_dir/source-keyring.json"
cp "$run_dir/keys/keyring.json" "$run_dir/restored-keyring.json"

# Positive control: a successful test on an empty destination would be a false pass.
if exercise verify >> "$run_dir/empty-database-control.log" 2>&1; then
    printf 'Empty database was incorrectly accepted.\n' >> "$log"
    exit 1
fi
# Check the failure reason, not merely a nonzero process status.
if ! grep -q 'restored table contents differ from backup manifest' "$run_dir/empty-database-control.log"; then
    printf 'Empty database control failed for an unexpected reason.\n' >> "$log"
    exit 1
fi
docker exec -i "$container_id" pg_restore -U postgres -d recovery_restored \
    --single-transaction --exit-on-error < "$run_dir/db/cycrio.dump" >> "$log" 2>&1
if ! exercise verify >> "$log" 2>&1; then exit 1; fi
printf 'Synthetic DB/key recovery verified. Report: %s/report.json\n' "$run_dir"
