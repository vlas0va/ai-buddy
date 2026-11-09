## Why
Accesses and channels are first-week items for both the intern and the mentor in the Avito checklist (CL-01, CL-04, CL-05).

## What Changes
- Required accesses with statuses and drafts for missing ordinary ones
- Elevated accesses only through manager approval
- Team channels and a draft request to the mentor
- Mentor view of what the intern requested

## Capabilities
### New Capabilities
- `access-and-channels`: The agent … (12 scenarios: S4-01 … S4-11)

### Modified Capabilities
- none

## Out of scope
Granting access in real systems.

## Impact
- Code: `services/agent/` (owner A). Contract: `contracts/agent-stream.md`.
- Tests: tests/test_access_s4.py, eval/cases/s4_access.yaml — every scenario ID appears in a test name.
