## Why
S3 is the admission scenario: it repeats the example of the case (day off) and proves the definition «action done» (R04, R17, R66, R79).

## What Changes
- Explain the procedure from the test instruction, collect dates in Russian, validate via absence.validate_request
- Show a draft (action_proposal) and never write in the same turn
- Confirm turn: absence.create_request with idempotency key, result shown and saved, idempotent retries
- Remind to talk to the mentor; only day_off is created (vacation and sick leave are explained)

## Capabilities
### New Capabilities
- `absence-request`: The agent … (20 scenarios: S3-01 … S3-11)

### Modified Capabilities
- none

## Out of scope
Approval UI (C), storage of actions (B).

## Impact
- Code: `services/agent/` (owner A). Contract: `contracts/agent-stream.md`.
- Tests: tests/test_absence_s3.py, tests/test_units.py (S3-02 dates), eval/cases/s3_absence.yaml — every scenario ID appears in a test name.
