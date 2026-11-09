# services/agent — owner A

Agent service, Python 3.12 + Flask (ADR-001). Contract: `contracts/agent-stream.md` (v1). Specs: `openspec/specs/*`.
- Entry: `agent/app.py` (`POST /v1/turn` message|confirm → SSE). Orchestration: `agent/orchestrator.py`. Flows: `agent/flows/`
  (absence=S3, access=S4, plan=S1, qa=S2 + refusals, team=lead/mentor). Router: `agent/nlu/router.py` (guardrails → rules →
  `intent_model.json` classifier). Tools: `agent/tools/` (gateway by role, MCP HTTP client, in-process mocks = B's schemas).
- `agent.yaml` is the single place for prompt, model, MCP servers and role → tools (R53). Never hardcode tools in prompts.
- Providers: `stub` (default, no network) and `openai_compat` (`LLM_*`). Same tool layer, same SSE events. The LLM gets READ tools only.
- Writes: only in a confirm turn with a valid token (format in `contracts/mcp-tools.md`), `idempotency_key = proposal_id`.
- KB results are ACL-filtered by mcp-kb AND re-checked against `data/kb` before reaching the LLM.
- Refusals (R72) and "remind, don't replace the mentor" (R05) live in guardrails, the prompt AND `eval/cases/guards.yaml`.
- Commands: `make test` · `make coverage` (≥80%, now ~97%) · `make eval` (guard 100%) · `make train` · `make run` (+ `/dev` console).
- Test names carry scenario IDs (`test_S3_05_...`); `python tools/spec_trace.py` must report every scenario traced.
