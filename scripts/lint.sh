#!/usr/bin/env bash
# gofmt, go vet, contracts check, openspec validate.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "== gofmt"
dirs=()
for d in services tools contracts migrations; do
  [ -d "$d" ] && dirs+=("$d")
done
unformatted="$(gofmt -l "${dirs[@]}" | tr -d '\r')"
if [ -n "$unformatted" ]; then
  echo "gofmt: these files need formatting (run: gofmt -w <file>):" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "== go vet"
go vet -mod=vendor ./services/... ./contracts/... ./migrations/...

echo "== contracts"
if python3 -c "" >/dev/null 2>&1; then PYTHON=python3; else PYTHON=python; fi
"$PYTHON" scripts/check_contracts.py

echo "== openspec"
if [ -d openspec ] && command -v openspec >/dev/null 2>&1; then
  openspec validate --all
else
  echo "skipped (no openspec directory or CLI)"
fi
echo "OK"
