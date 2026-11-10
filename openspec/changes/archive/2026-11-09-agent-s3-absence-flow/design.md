## Context
See ADR-001…ADR-008 in `docs/adr/` and `contracts/agent-stream.md`.

## Decisions
Deterministic finite-state flow (ADR-007) with slots in Redis/memory per session; validation by the absence service.

## Risks / Trade-offs
- Deterministic flows for writes trade some flexibility for safety and predictability on any model (ADR-007).
