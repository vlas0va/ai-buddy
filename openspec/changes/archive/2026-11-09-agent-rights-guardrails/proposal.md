## Why
The assistant must never disclose or act on data the user may not access, and must keep sensitive topics out of scope (R22, R71, R72, ADR-003).

## What Changes
- Deterministic guardrails before any tool or LLM call
- Role allowlist of tools from agent.yaml; write tools never offered to the LLM
- Agent-side ACL re-check of KB results; sanitising instructions found inside documents
- Rights matrix of the in-process mocks (mirror of contracts/mcp-tools.md) and confirmation tokens

## Capabilities
### New Capabilities
- `rights-guardrails`: The agent … (33 scenarios: RG-01 … CT-03)

### Modified Capabilities
- none

## Out of scope
Authentication itself (existing Avito IdP, imitated by B).

## Impact
- Code: `services/agent/` (owner A). Contract: `contracts/agent-stream.md`.
- Tests: tests/test_rights_rg.py, tests/test_mocks.py, tests/test_units.py, eval/cases/guards.yaml — every scenario ID appears in a test name.
