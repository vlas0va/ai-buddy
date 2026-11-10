#!/usr/bin/env bash
# S3 + S4 smoke through core on a running stack (make up). Usage: scripts/smoke.sh [CORE_URL]
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if python3 -c "" >/dev/null 2>&1; then PY=python3; else PY=python; fi
exec "$PY" "$ROOT/scripts/smoke.py" "${1:-http://localhost:8080}"
