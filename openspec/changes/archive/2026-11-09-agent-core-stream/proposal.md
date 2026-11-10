## Why
Core and web need one stable, non-blocking contract for chat turns, statuses and failures (R27, R53, R77–R79).

## What Changes
- POST /v1/turn (message and confirm turns) streaming SSE events of contracts/agent-stream.md
- Identity from the user JWT, context mismatch rejection, optional service token
- Declarative agent.yaml (prompt, model, MCP servers, role → tools); health, readiness, metrics; auxiliary endpoints (reminders, summary, FAQ draft, config, KB document)

## Capabilities
### New Capabilities
- `agent-behavior`: The agent … (16 scenarios: AG-07 … AG-10)

### Modified Capabilities
- none

## Out of scope
UI rendering (C), persistence of messages and actions (B).

## Impact
- Code: `services/agent/` (owner A). Contract: `contracts/agent-stream.md`.
- Tests: tests/test_app_ag.py, tests/test_units.py — every scenario ID appears in a test name.
