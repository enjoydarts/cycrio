#!/bin/sh
# Simulate losing the Compose CLI after daemon-side creation but before its ID is returned.
set -eu
umask 077
cd "$(dirname "$0")/../.."
mkdir -p .local/recovery
fixture=$(mktemp -d .local/recovery/cleanup.XXXXXXXX)
docker_bin=$(command -v docker)
cleanup() {
    status=$?
    trap - 0
    if [ -s "$fixture/container-id" ]; then
        docker rm -f "$(cat "$fixture/container-id")" >> "$fixture/cleanup.log" 2>&1 || true
    fi
    exit "$status"
}
trap cleanup 0
trap 'exit 130' INT
trap 'exit 143' TERM HUP
mkdir "$fixture/bin"
cat > "$fixture/bin/docker" <<'SH'
#!/bin/sh
if [ "$#" -ge 4 ] && [ "$1" = compose ] && [ "$2" = run ] && [ "$3" = --rm ] && [ "$4" = -d ]; then
    "$RECOVERY_TEST_DOCKER" "$@" > "$RECOVERY_TEST_FIXTURE/container-id" || exit 2
    exit 1
fi
exec "$RECOVERY_TEST_DOCKER" "$@"
SH
chmod 0700 "$fixture/bin/docker"
if PATH="$PWD/$fixture/bin:$PATH" RECOVERY_TEST_DOCKER="$docker_bin" RECOVERY_TEST_FIXTURE="$PWD/$fixture" \
    sh deploy/local/recover-test.sh > "$fixture/runner.log" 2>&1; then
    printf 'Recovery runner incorrectly accepted a failed Compose CLI.\n' >&2
    exit 1
fi
if [ ! -s "$fixture/container-id" ]; then
    printf 'Cleanup control did not create its disposable container. See %s\n' "$fixture/runner.log" >&2
    exit 1
fi
if docker inspect "$(cat "$fixture/container-id")" > /dev/null 2>&1; then
    printf 'Recovery runner left its started database behind after CLI failure.\n' >&2
    exit 1
fi
printf 'Recovery cleanup after lost CLI output verified.\n'
