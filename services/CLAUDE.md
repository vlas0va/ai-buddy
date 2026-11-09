# services — Go backend, owner B (agent service in services/agent is owner A)

One Go module `aibuddy` at the repo root, vendored dependencies (`go build -mod=vendor`).
Read `docs/backend/IMPLEMENTATION.md` before changing anything here: layout, package owners, behaviour per tool/endpoint.
- Binaries: `services/cmd/core` (8080) and `services/cmd/mcp-mocks` (8100, six MCP servers at /mcp/<server>).
- Identity only from verified JWT claims; write tools only with a valid confirmation token (R71, R79).
- Action = MCP result shown in chat AND saved in core.actions linked to a plan task (R66).
- Tests: `make test` (disposable Postgres via scripts/test-db.sh), `make coverage` (>= 80% per service). Test names carry IDs.
