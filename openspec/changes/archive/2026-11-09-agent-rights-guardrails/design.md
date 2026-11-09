## Context
See ADR-001…ADR-008 in `docs/adr/` and `contracts/agent-stream.md`.

## Decisions
Five layers: guardrails → ACL before LLM → tools by role → MCP re-check → confirmed write with HMAC token (ADR-003).

## Risks / Trade-offs
- Deterministic flows for writes trade some flexibility for safety and predictability on any model (ADR-007).
