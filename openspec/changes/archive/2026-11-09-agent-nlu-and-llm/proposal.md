## Why
The prototype must work without a key and with any OpenAI-compatible model, and the intent router must be measurable and retrainable (R44, R45, ADR-004, ADR-008).

## What Changes
- Softmax logistic-regression intent model trained from eval/intents, report with CV and a held-out set
- OpenAI-compatible client (stream, complete, tool calls), embeddings client
- MCP streamable-HTTP client with handshake, token forwarding and error mapping

## Capabilities
### New Capabilities
- `nlu-and-llm`: The agent … (13 scenarios: NLU-01 … MCP-14)

### Modified Capabilities
- none

## Out of scope
Fine-tuning of LLMs (not needed for the prototype).

## Impact
- Code: `services/agent/` (owner A). Contract: `contracts/agent-stream.md`.
- Tests: tests/test_training.py, tests/test_integrations.py — every scenario ID appears in a test name.
