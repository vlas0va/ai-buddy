## Context
See ADR-001…ADR-008 in `docs/adr/` and `contracts/agent-stream.md`.

## Decisions
Approval ids from approvals.list_pending; decision args {approval_id, decision, comment} per contracts/schemas/approvals.json.

## Risks / Trade-offs
- Deterministic flows for writes trade some flexibility for safety and predictability on any model (ADR-007).
