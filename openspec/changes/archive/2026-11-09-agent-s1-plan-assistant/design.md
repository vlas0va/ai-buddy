## Context
See ADR-001…ADR-008 in `docs/adr/` and `contracts/agent-stream.md`.

## Decisions
Pure functions over the plan; CTA per checklist item; templates for reminders so AI-cron works without a key.

## Risks / Trade-offs
- Deterministic flows for writes trade some flexibility for safety and predictability on any model (ADR-007).
