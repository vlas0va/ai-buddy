#!/usr/bin/env bash
# Playwright e2e на запущенном стеке (make up). По одной спеке на заявленный сценарий (S1-S4) + ограничения и роли.
# WEB_URL (по умолчанию http://localhost:5173) и API_URL (по умолчанию http://localhost:8080) указывают на стек.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT/e2e"
[ -d node_modules ] || npm ci
npx playwright install chromium >/dev/null
npx playwright test "$@"
