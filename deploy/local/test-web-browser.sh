#!/bin/sh
set -eu
# Every invocation owns its containers and network, including concurrent CI runs.
project="cycrio-browser-$(date +%s)-$$"
compose() { docker compose -p "$project" -f compose.browser.yaml "$@"; }
mkdir -p artifacts/browser
rm -f artifacts/browser/failure.png artifacts/browser/diagnostics.json \
  artifacts/browser/results.json artifacts/browser/compose.log artifacts/browser/containers.txt
cleanup() {
  result=$?
  trap - EXIT INT TERM
  if [ "$result" -ne 0 ]; then
    compose logs --no-color > artifacts/browser/compose.log 2>&1 || true
    compose ps -a > artifacts/browser/containers.txt 2>&1 || true
  fi
  compose down --volumes --remove-orphans || result=1
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
compose config --quiet
compose build fixture
compose up -d --wait --wait-timeout 180 fixture browser
compose run --rm --no-deps tests
