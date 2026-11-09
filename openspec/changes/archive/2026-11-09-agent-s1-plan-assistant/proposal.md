## Why
Interns must see stages, task statuses and the next step; mentors and leads need their views; AI-cron needs reminder texts (R18–R20, R54, R58).

## What Changes
- Stage, done/total, overdue and next step from the plan sent by core or the checklist template
- next_step event with a call to action
- Mentor and lead views, alumni without a plan
- Reminder texts for AI-cron and the equator progress summary

## Capabilities
### New Capabilities
- `plan-assistant`: The agent … (14 scenarios: S1-01 … S1-13)

### Modified Capabilities
- none

## Out of scope
Plan storage and status changes (core, B); timeline UI (C).

## Impact
- Code: `services/agent/` (owner A). Contract: `contracts/agent-stream.md`.
- Tests: tests/test_plan_s1.py, eval/cases/s1_plan.yaml — every scenario ID appears in a test name.
