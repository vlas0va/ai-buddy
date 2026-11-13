#!/usr/bin/env bash
# Юнит- и интеграционные тесты Go.  Запуск: scripts/test.sh [доп. аргументы go test, например -tags demo]
# Без TEST_DATABASE_URL поднимается одноразовый Postgres (scripts/test-db.sh) и остаётся работать для быстрых
# повторных запусков; остановить — `make test-db-down`.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [ -z "${TEST_DATABASE_URL:-}" ]; then
  TEST_DATABASE_URL="$(bash "$ROOT/scripts/test-db.sh" up | tail -n 1 | tr -d '\r')"
  export TEST_DATABASE_URL
fi

log="$(mktemp)"
trap 'rm -f "$log"' EXIT

status=0
go test -mod=vendor -count=1 "$@" ./services/... ./contracts/... ./migrations/... >"$log" 2>&1 || status=$?

# Короткий вывод: только падения и сводка по пакетам.
if [ "$status" -ne 0 ]; then
  grep -v -E '^ok |no test files' "$log" | tail -n 60 || true
  echo "FAIL: go test exited with status $status" >&2
  exit "$status"
fi
grep -c -E '^ok ' "$log" | sed 's/^/packages ok: /' || true
echo "OK"
