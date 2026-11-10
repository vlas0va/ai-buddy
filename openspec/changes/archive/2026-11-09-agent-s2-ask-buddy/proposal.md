## Why
Interns need answers about their team and tools with context and sources, and a fast path to a human when the KB has no answer (R02, R03, R05, R10, R15, R69).

## What Changes
- kb.search with ACL, agent-side re-ranking by team and plan tasks, coverage threshold
- Answers with sources and the «Тестовая инструкция прототипа» badge; stub (extractive) or LLM (grounded)
- Escalation drafts to mentor, lead, curator or HR BP; contacts from the directory
- Human-only topics: remind and help prepare

## Capabilities
### New Capabilities
- `ask-buddy`: The agent … (21 scenarios: S2-01 … S2-21)

### Modified Capabilities
- none

## Out of scope
Sending escalations and adding answers to the KB (core, B); rendering (C).

## Impact
- Code: `services/agent/` (owner A). Contract: `contracts/agent-stream.md`.
- Tests: tests/test_qa_s2.py, tests/test_integrations.py, tests/test_units.py, eval/cases/s2_qa.yaml — every scenario ID appears in a test name.
