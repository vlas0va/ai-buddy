#!/usr/bin/env bash
# Playwright e2e against a running stack (make up). One spec per declared scenario (S1-S4) + guards and roles.
# WEB_URL (default http://localhost:5173) and API_URL (default http://localhost:8080) point at the stack.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT/e2e"
[ -d node_modules ] || npm ci
npx playwright install chromium >/dev/null
npx playwright test "$@"
