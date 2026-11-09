#!/usr/bin/env bash
# Per-service Go coverage gate (>= 80%). Counted: ./internal/... except ./internal/gen/... (generated code, R73).
# cmd/ mains are excluded by construction.
# Agent (Python) coverage is included below (HTML + Cobertura in coverage/agent). TODO: Go Cobertura, web.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
THRESHOLD=80.0
fail=0
rows=""

for dir in "$ROOT"/services/*/; do
  [ -f "$dir/go.mod" ] || continue
  svc="$(basename "$dir")"
  out="$ROOT/coverage/$svc"
  mkdir -p "$out"
  pkgs="$(cd "$dir" && go list ./internal/... 2>/dev/null | grep -v '/internal/gen' | paste -sd, -)"
  [ -n "$pkgs" ] || { echo "no internal packages in $svc" >&2; exit 1; }
  (cd "$dir" && go test ./... -coverpkg="$pkgs" -coverprofile="$out/cover.out" >"$out/test.log" 2>&1) || {
    tail -30 "$out/test.log"
    echo "tests failed in $svc" >&2
    exit 1
  }
  (cd "$dir" && go tool cover -html="$out/cover.out" -o "$out/index.html")
  total="$(cd "$dir" && go tool cover -func="$out/cover.out" | awk '/^total:/ {gsub("%","",$3); print $3}')"
  rows="$rows$(printf '%-16s | %s' "$svc" "$total")"$'\n'
  if awk -v t="$total" -v th="$THRESHOLD" 'BEGIN{exit !(t+0 < th+0)}'; then
    fail=1
  fi
done

# Agent (Python, owner A): pytest-cov over services/agent/{agent,training}; tests and the model JSON are excluded.
if [ -f "$ROOT/services/agent/requirements.txt" ]; then
  out="$ROOT/coverage/agent"
  mkdir -p "$out"
  (cd "$ROOT/services/agent" && python3 -m pytest -q --cov --cov-report=html:"$out/html" \
      --cov-report=xml:"$out/cobertura.xml" --cov-report=term >"$out/test.log" 2>&1) || {
    tail -30 "$out/test.log"; echo "tests failed in agent" >&2; exit 1; }
  total="$(awk '/^TOTAL/ {gsub("%","",$NF); print $NF}' "$out/test.log")"
  rows="$rows$(printf '%-16s | %s' "agent" "$total")"$'\n'
  if awk -v t="$total" -v th="$THRESHOLD" 'BEGIN{exit !(t+0 < th+0)}'; then fail=1; fi
fi

printf '%-16s | %s\n' "service" "total %"
printf '%s\n' "-----------------+--------"
printf '%s' "$rows"
if [ "$fail" -ne 0 ]; then
  echo "FAIL: at least one service is below ${THRESHOLD}%" >&2
  exit 1
fi
echo "OK: all services >= ${THRESHOLD}%"
