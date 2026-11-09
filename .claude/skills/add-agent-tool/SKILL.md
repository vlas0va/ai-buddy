---
name: add-agent-tool
description: Wire an MCP tool into the agent with role permissions, confirmation policy, SSE events and eval cases. Use when the agent must start using a new or changed MCP tool.
---
# Add a tool to the agent

1. Spec: `/opsx:propose agent-tool-<name>`. The spec states when the agent calls the tool, which roles may, whether it writes,
   what the user sees (SSE events from `contracts/agent-stream.md`), and refusal cases. Scenario IDs go into test names.
2. Config: add the tool under its MCP server in `services/agent/agent.yaml` with `roles: [...]` and `write: true|false`.
   The tool list sent to the LLM is built from this file and the user's role — never hardcode tools in prompts.
3. Write tools: the agent emits `action_proposal` and STOPS. It calls the tool only after core forwards a confirmed proposal
   with `confirmation_token`; `idempotency_key = proposal_id`.
4. Stub path: add the intent to the stub router (keywords, slots, Russian date parsing) so the scenario passes without a key.
5. Status events: emit `status{stage: calling_service}` before the call; map MCP errors to `error{code, message, retryable}`.
6. Eval: add at least 5 cases to `eval/cases/<tool>.yaml` — happy path, missing slot, denied role, injection attempt, service failure.
7. Tests: unit for tool selection by role, proposal/confirm flow, error mapping; run `make eval` on stub.
8. Update `docs/context/architecture.md` if the tool table changed.
