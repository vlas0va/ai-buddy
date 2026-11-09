---
name: coverage-gate
description: Measure line coverage for all services, list files below 80% and close the gaps. Use before every PR that adds code and in the quality phase.
---
# Coverage gate (R73, R74)

1. Run `make coverage 2>&1 | tail -40`. It writes `coverage/<service>/` (HTML + Cobertura) and prints a summary table.
2. If a service is below 80%, list its 10 least-covered files:
   - Go: `go tool cover -func=coverage/<svc>/cover.out | sort -k3 -n | head -10`
   - Python: `coverage report --sort=cover | head -15`
   - Web: read `coverage/web/coverage-summary.json` (lines.pct per file), sort ascending.
3. Check exclusions are honest: only dependencies, generated code (`internal/gen`, generated API types), entrypoints (`cmd/*/main.go`, `main.tsx`) and test files. Never exclude application logic to pass the gate.
4. Delegate gap-closing to the `test-writer` subagent, ONE package per call, sequentially.
5. Re-run `make coverage`. The report in `coverage/` must be reproducible from a clean checkout with the same command; mention that command in README.
