#!/bin/sh
set -eu

if [ -n "${DATABASE_URL_FILE:-}" ]; then
  DATABASE_URL="$(cat "$DATABASE_URL_FILE")"
  export DATABASE_URL
fi

if [ -n "${HATCHET_CLIENT_TOKEN_FILE:-}" ]; then
  HATCHET_CLIENT_TOKEN="$(cat "$HATCHET_CLIENT_TOKEN_FILE")"
  export HATCHET_CLIENT_TOKEN
fi

component="${1:?component is required}"
shift
case "$component" in
  api|worker|devcheck|manage) ;;
  *) echo 'Unknown application component.' >&2; exit 2 ;;
esac

go build -o "/tmp/cycrio-$component" "./cmd/$component"
exec "/tmp/cycrio-$component" "$@"
