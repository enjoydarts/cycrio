#!/bin/sh
set -eu

HATCHET_CLIENT_TOKEN="$(cat "${HATCHET_CLIENT_TOKEN_FILE:?Hatchet token file is required}")"
export HATCHET_CLIENT_TOKEN
exec go test -tags=integration,evaluation,hatchet_evaluation ./internal/database \
  -run '^TestHatchet(Evaluation|HistoryPositiveControl)$' -count=1 -timeout=4m -v -args \
  -hatchet-evaluation-dir="${1:?private evaluation directory is required}" \
  -hatchet-evaluation-database='postgres://hatchet:hatchet-local@hatchet-postgres:5432/hatchet?sslmode=disable'
