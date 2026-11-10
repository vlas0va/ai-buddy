## Context
See ADR-001…ADR-008 in `docs/adr/` and `contracts/agent-stream.md`.

## Decisions
Flask + gunicorn gthread; generator-based SSE; pydantic request models; contextvar clock; Prometheus text metrics without dependencies.

## Risks / Trade-offs
- Deterministic flows for writes trade some flexibility for safety and predictability on any model (ADR-007).
