#!/usr/bin/env bash
# Playwright e2e against a running stack (make up). Owner: C.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -f "$ROOT/e2e/package.json" ]; then
  cd "$ROOT/e2e"
  npx playwright test
else
  echo "e2e/ is owned by C and does not exist yet (no e2e/package.json): nothing to run."
fi
