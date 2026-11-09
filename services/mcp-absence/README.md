# mcp-absence

Mock corporate absence service (owner B). Currently only `/healthz` and a `-healthcheck` flag.
The MCP tools from `contracts/mcp-tools.md` will be implemented next
with the official MCP Go SDK, using the `add-mock-service` skill.
Port: `MCP_ABSENCE_PORT` (default 8101). Tests: `go test ./...`.
