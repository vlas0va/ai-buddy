#!/usr/bin/env bash
# Порог покрытия Go по сервисам (>= 80%, R73).  Отчёты: coverage/<service>/cover.out и index.html.
# Наборы сервисов: platform, mocks, core. Сгенерированный код (/internal/gen/) и main из cmd/ не считаются.
# Без TEST_DATABASE_URL поднимается одноразовый Postgres (scripts/test-db.sh).
# Агент (Python, владелец A) меряется через pytest-cov в coverage/agent (HTML + Cobertura).
# Веб (владелец C) проверяет свой порог в web/vite.config.ts (задача CI web).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
THRESHOLD=80.0

if [ -z "${TEST_DATABASE_URL:-}" ]; then
  TEST_DATABASE_URL="$(bash "$ROOT/scripts/test-db.sh" up | tail -n 1 | tr -d '\r')"
  export TEST_DATABASE_URL
fi

declare -a NAMES=(platform mocks core)
declare -a PATTERNS=(
  "./services/internal/platform/..."
  "./services/internal/mocks/..."
  "./services/internal/core/..."
)

fail=0
rows=""

for i in "${!NAMES[@]}"; do
  svc="${NAMES[$i]}"
  pattern="${PATTERNS[$i]}"
  out="$ROOT/coverage/$svc"

  listing="$(go list -mod=vendor "$pattern" 2>&1 | tr -d '\r' || true)"
  pkgs="$(printf '%s\n' "$listing" | grep -E '^aibuddy/' | grep -v '/internal/gen/' | paste -sd, - || true)"
  if [ -z "$pkgs" ]; then
    if printf '%s' "$listing" | grep -q -E 'matched no packages|no Go files|^$'; then
      echo "note: no packages yet for '$svc' ($pattern), skipped" >&2
      rows="$rows$(printf '%-10s | %s' "$svc" "skipped (no packages)")"$'\n'
      continue
    fi
    printf '%s\n' "$listing" >&2
    echo "go list failed for $svc" >&2
    exit 1
  fi

  mkdir -p "$out"
  if ! go test -mod=vendor -count=1 "$pattern" -coverpkg="$pkgs" -coverprofile="$out/cover.out" >"$out/test.log" 2>&1; then
    grep -v -E '^ok |no test files' "$out/test.log" | tail -n 40 || true
    echo "tests failed in $svc" >&2
    exit 1
  fi
  go tool cover -html="$out/cover.out" -o "$out/index.html"
  total="$(go tool cover -func="$out/cover.out" | awk '/^total:/ {gsub("%","",$3); print $3}' | tr -d '\r')"
  rows="$rows$(printf '%-10s | %s' "$svc" "$total")"$'\n'
  if awk -v t="$total" -v th="$THRESHOLD" 'BEGIN{exit !(t+0 < th+0)}'; then
    fail=1
  fi
done

# Агент (Python, владелец A): pytest-cov по services/agent/{agent,training}; тесты и JSON модели исключены.
if [ -f "$ROOT/services/agent/requirements.txt" ]; then
  if python3 -c "" >/dev/null 2>&1; then PY=python3; else PY=python; fi
  out="$ROOT/coverage/agent"
  mkdir -p "$out"
  (cd "$ROOT/services/agent" && "$PY" -m pytest -q --cov --cov-report=html:"$out/html" \
      --cov-report=xml:"$out/cobertura.xml" --cov-report=term >"$out/test.log" 2>&1) || {
    tail -30 "$out/test.log"; echo "tests failed in agent" >&2; exit 1; }
  total="$(awk '/^TOTAL/ {gsub("%","",$NF); print $NF}' "$out/test.log")"
  rows="$rows$(printf '%-10s | %s' "agent" "$total")"$'\n'
  if awk -v t="$total" -v th="$THRESHOLD" 'BEGIN{exit !(t+0 < th+0)}'; then fail=1; fi
fi

printf '%-10s | %s\n' "service" "total %"
printf '%s\n' "-----------+--------"
printf '%s' "$rows"
if [ "$fail" -ne 0 ]; then
  echo "FAIL: at least one service is below ${THRESHOLD}%" >&2
  exit 1
fi
echo "OK: all measured services >= ${THRESHOLD}%. Reports: coverage/<service>/index.html"
