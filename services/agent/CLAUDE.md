# services/agent — owner A

Agent service. Language per ADR-001 (Python 3.12 + Flask, or Go). Contract: `contracts/agent-stream.md`.
- `agent.yaml` is the single place for system prompt path, model, MCP servers and role → tools. Mirrors Avito's internal "prompt + MCP" agent tool (R53).
- Providers: `stub` (default, deterministic, no network) and `openai_compat` (env `LLM_*`). Both go through the SAME tool layer and emit the SAME SSE events.
- Never call a write tool without a confirmed proposal and `confirmation_token`. Never offer the LLM tools outside the user's role.
- Retrieval results come from `mcp-kb` already ACL-filtered; do not add your own unfiltered DB access.
- Refusals (R72) and "remind, don't replace the mentor" rules (R05) live in the prompt AND in eval cases (`eval/cases/`).
- Tests: `pytest -q` (or `go test ./...`), eval: `make eval` on stub. Test names carry scenario IDs.
