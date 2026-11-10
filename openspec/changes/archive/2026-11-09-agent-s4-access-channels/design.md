## Context
See ADR-001…ADR-008 in `docs/adr/` and `contracts/agent-stream.md`.

## Decisions
Resource resolution by names and synonyms with preference for non-elevated resources unless the user asks for a closed one.

## Risks / Trade-offs
- Deterministic flows for writes trade some flexibility for safety and predictability on any model (ADR-007).
