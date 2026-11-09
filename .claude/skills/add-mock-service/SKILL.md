---
name: add-mock-service
description: Create a new mock corporate service exposed as an MCP server (absence, approvals, access, checklist, directory, kb or a new one). Use whenever a scenario needs a corporate system that does not exist yet.
---
# Add a mock corporate service behind MCP

Why: the case says to imitate Avito systems and perform actions through MCP instead of rebuilding them (R39, R52, R56).

## Steps
1. Contract first. Add the tools to `contracts/mcp-tools.md` and a JSON Schema per tool in `contracts/schemas/<service>/<tool>.json`
   (inputs, outputs, error codes). Mark write tools. Open a PR if the contract is new.
2. Scaffold `services/mcp-<service>/` from an existing server (copy the layout of `services/mcp-absence/`):
   `cmd/server/main.go`, `internal/store` (own PG schema `mock_<service>`), `internal/tools`, `internal/authz`.
3. Auth in the server, not only in the agent: parse the user JWT from the request, check role, ownership and team for EVERY tool.
4. Write tools additionally require `confirmation_token` (verify HMAC, user, payload hash, expiry) and `idempotency_key`
   (unique index; a retry returns the stored result).
5. Seed data in `data/seed/<service>.sql` or `.yaml` — synthetic names only; at least two teams.
6. Compose: add the service to `deploy/docker-compose.yml` under profile `core`, with a healthcheck and env from `.env.example`.
7. Register the server in `services/agent/agent.yaml` (url, tools, roles per tool).
8. Tests (Go, table-driven):
   - happy path per tool;
   - role × tool × resource matrix: allowed and denied cases (R71);
   - write without/with invalid confirmation token → denied;
   - same idempotency key twice → one record (R79);
   - storage failure → typed error, no partial write.
9. `go test ./... -cover` for the server ≥ 80%. Add the service to `make coverage`.
10. Update `docs/context/architecture.md` MCP table in the same PR.

## Done when
Contract merged, server healthy in `make up`, agent can call it on stub LLM, rights matrix green, coverage ≥ 80%.
