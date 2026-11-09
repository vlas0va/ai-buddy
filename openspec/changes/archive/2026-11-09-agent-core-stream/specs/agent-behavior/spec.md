## ADDED Requirements

### Requirement: Turn endpoint streams contract events
The agent SHALL answer POST /v1/turn with text/event-stream, emit a status event first and exactly one terminal event (done or error) last.

#### Scenario: AG-07 Exactly one terminal event
- **WHEN** any message turn of any scenario
- **THEN** the first event is status and the stream ends with exactly one done or error

#### Scenario: AG-16 Status stages stay in the contract enum
- **WHEN** the agent reports progress
- **THEN** status.stage is one of thinking, searching_kb, calling_service, saving and status.text carries the Russian label

#### Scenario: AG-08 Request id is echoed
- **WHEN** core sends X-Request-Id
- **THEN** the response header and done.request_id carry the same id

### Requirement: Identity comes from the JWT
The agent SHALL take user_id, role and team_id from the verified JWT and SHALL reject requests whose user_context disagrees with the claims.

#### Scenario: AG-02 Missing or invalid token
- **WHEN** a request has no or an invalid bearer token
- **THEN** the agent answers 401 unauthorized

#### Scenario: AG-03 Context mismatch
- **WHEN** user_context.role differs from the JWT role
- **THEN** the agent answers 403 context_mismatch

#### Scenario: AG-13 Expired token
- **WHEN** the JWT exp is in the past
- **THEN** the agent answers 401

#### Scenario: AG-15 Unknown role
- **WHEN** the JWT role is not a known role
- **THEN** the agent answers 401

#### Scenario: AG-05 Service token
- **WHEN** AGENT_SERVICE_TOKEN is configured
- **THEN** requests without the matching X-Service-Token header are rejected with 401

### Requirement: Robust input handling and failures
The agent SHALL validate request bodies and SHALL turn tool and internal failures into error events without leaving the stream open.

#### Scenario: AG-04 Malformed bodies
- **WHEN** the body is not JSON, the message is empty or a confirm turn lacks fields
- **THEN** the agent answers 400 with a code

#### Scenario: AG-11 Internal error
- **WHEN** an unexpected exception happens inside a flow
- **THEN** the stream ends with error{code: internal, retryable: true}

#### Scenario: AG-12 Tool failure is retryable
- **WHEN** an MCP call fails
- **THEN** the stream ends with error{retryable: true} and a repeated message succeeds

### Requirement: Declarative configuration and operations
The agent SHALL be configured by agent.yaml with environment substitution and SHALL expose health, readiness, metrics and a sanitized config per role.

#### Scenario: AG-01 Health, readiness, metrics
- **WHEN** the service runs
- **THEN** /healthz is ok, /readyz reports the KB size and the intent model, /metrics exposes turn counters and latency histograms

#### Scenario: AG-06 Config per role
- **WHEN** an intern requests /v1/agent/config
- **THEN** the answer lists only the intern read tools and the write tools reachable via confirmation

#### Scenario: AG-18 Config validation
- **WHEN** a write tool lacks proposal_kind or env variables are set
- **THEN** loading fails for the invalid tool and ${VAR:-default} values are substituted

#### Scenario: AG-09 Clock override for demo
- **WHEN** AGENT_ALLOW_CLOCK_OVERRIDE is on and the request has today
- **THEN** relative dates are resolved against that date

#### Scenario: AG-10 FAQ draft from a mentor answer
- **WHEN** a mentor sends a question and answer of an escalation
- **THEN** the agent returns a KB document draft with source_type test for the mentor team; interns and foreign teams get 403
