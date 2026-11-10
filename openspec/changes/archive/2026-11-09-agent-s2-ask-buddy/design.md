## Context
See ADR-001…ADR-008 in `docs/adr/` and `contracts/agent-stream.md`.

## Decisions
BM25 + Snowball stemming, optional embeddings with RRF; coverage threshold against generic matches; prompts system.v1 and answer.v1 (ADR-008).

## Risks / Trade-offs
- Deterministic flows for writes trade some flexibility for safety and predictability on any model (ADR-007).
