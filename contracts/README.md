# Contracts

Agreements between the three zones. Implement against these files; change them only through a PR approved by the owner.

| File | Between | Owner | Status |
|---|---|---|---|
| `openapi.yaml` | web ↔ core | B | v0.1 — S1, S3 complete; S2, S4 endpoints present |
| `agent-stream.md` | agent → core → web | A | draft by B, A to confirm |
| `mcp-tools.md` + `schemas/*.json` | agent ↔ MCP servers | B | v0.1 — all six servers described |
| `events.md` | everyone → analytics | B | v0.1 |
| `../migrations/*.sql` | core, MCP servers | B | 0001 core, 0002 mock services, 0003 demo seed (generated) |

Rules
- A breaking change bumps the version line in the file and is announced in the team chat.
- Every enum here is the single source for code: Zod schemas in web, Go types in core and MCP servers, pydantic models in agent.
- `make seed` regenerates `migrations/0003_seed_demo.sql` from `data/checklist/checklist.yaml`; `make reset` recreates the database.
