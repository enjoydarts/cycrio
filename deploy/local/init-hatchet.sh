#!/bin/sh
set -eu
umask 077

if [ -s /token/token ] && [ "${HATCHET_ROTATE_TOKEN:-false}" != true ]; then
  echo 'Hatchet worker token is already initialized.'
  exit 0
fi

temporary_file="$(mktemp /token/token.XXXXXX)"
trap 'rm -f "$temporary_file"' EXIT HUP INT TERM
/hatchet-admin token create --config /config --name cycrio-local > "$temporary_file"
test -s "$temporary_file"
chmod 0444 "$temporary_file"
mv "$temporary_file" /token/token
echo 'Hatchet worker token initialized.'
