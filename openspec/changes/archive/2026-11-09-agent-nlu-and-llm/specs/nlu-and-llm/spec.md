## ADDED Requirements

### Requirement: Trainable intent router
The agent SHALL route messages through guardrails, high-precision rules and a trained classifier with a confidence threshold, and the training SHALL report accuracy on a held-out set.

#### Scenario: NLU-01 Train, save, load
- **WHEN** the model is trained on samples
- **THEN** it predicts the training intents after a save/load round-trip and rejects unknown formats

#### Scenario: NLU-02 Training script and quality
- **WHEN** the training script runs
- **THEN** the report is written and the full router reaches at least 85% on the held-out set

#### Scenario: NLU-03 Cross-validation
- **WHEN** cross-validation runs
- **THEN** logistic regression and the naive Bayes baseline are scored

#### Scenario: NLU-04 No model
- **WHEN** the model file is missing
- **THEN** the router falls back to rules and kb_question

### Requirement: OpenAI-compatible provider
The agent SHALL stream tokens from any OpenAI-compatible endpoint and SHALL map HTTP errors and timeouts to retryable or non-retryable errors.

#### Scenario: LLM-01 Stream and complete
- **WHEN** the endpoint streams chunks
- **THEN** tokens are joined, broken lines are skipped and complete returns the message

#### Scenario: LLM-02 HTTP errors
- **WHEN** the endpoint answers 429, 500 or 400
- **THEN** LLMError with retryable true, true, false

#### Scenario: LLM-03 Timeouts
- **WHEN** the endpoint times out or is down
- **THEN** llm_timeout or llm_unavailable

#### Scenario: LLM-04 Provider selection
- **WHEN** LLM_PROVIDER is stub or openai_compat
- **THEN** the stub or the LLM writer is used

#### Scenario: LLM-05 Embeddings
- **WHEN** the embeddings endpoint answers
- **THEN** vectors are returned in input order

### Requirement: MCP client
The agent SHALL reach MCP servers over streamable HTTP, forward the user token on every request and map tool errors of contracts/mcp-tools.md.

#### Scenario: MCP-11 Handshake and results
- **WHEN** the agent calls a tool
- **THEN** initialize precedes tools/call, the bearer token and Mcp-Session-Id are sent, JSON and SSE responses are parsed

#### Scenario: MCP-12 Tool errors
- **WHEN** the server returns isError with a code
- **THEN** forbidden, invalid_confirmation, conflict, validation_error and unavailable map to the agent errors

#### Scenario: MCP-13 HTTP failures
- **WHEN** the server answers 404, 401, 503, times out or returns a JSON-RPC error
- **THEN** the session is re-initialised once or a mapped error is raised

#### Scenario: MCP-14 HTTP backend in the agent
- **WHEN** MCP_KB_URL is set
- **THEN** kb.search goes over HTTP and the agent drops documents the user may not read
