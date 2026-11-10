## Why
Mentor and lead take part in adaptation (R57, R67): leads approve requests, mentors follow interns — in the same interface.

## What Changes
- Lead: list pending approvals and decide in chat through a confirmed approval_decision proposal
- Mentor: progress of own mentees from user_context.mentees

## Capabilities
### New Capabilities
- `team-roles`: The agent … (8 scenarios: TR-01 … TR-08)

### Modified Capabilities
- none

## Out of scope
Separate interfaces for mentors and leads.

## Impact
- Code: `services/agent/` (owner A). Contract: `contracts/agent-stream.md`.
- Tests: tests/test_team_tr.py, eval/cases/team_whois.yaml — every scenario ID appears in a test name.
