# OpenSpec seed

## 1. Init (account A, phase 0)
```
npm install -g @fission-ai/openspec@latest   # Node >= 20.19
openspec init --tools claude                 # creates openspec/ and .claude/skills/openspec-*
openspec validate --all                      # also runs in CI
```
Core commands in Claude Code: `/opsx:propose <change>`, `/opsx:apply`, `/opsx:archive`, `/opsx:explore`.
The expanded profile adds `/opsx:verify` (useful before archive): `openspec config profile` → `openspec update`.

## 2. Project context for OpenSpec
After `openspec init`, open the generated `openspec/config.yaml` and put the block below into its project-context field
(check the field name in the generated file; keep it short — OpenSpec sends it with every planning request).
```
AI-buddy: prototype AI mentor for interns (Avito Start case, Hack&Change 2026).
Stack: Go core + Go MCP servers (mock corporate services), agent service (Python+Flask or Go, ADR-001),
React+TS+TanStack+Zustand+Zod, PostgreSQL, Redis, Kafka, Grafana, Docker Compose, GitHub Actions.
Hard rules: works without LLM key (stub default); rights enforced before LLM, per role tool list, and inside every MCP server;
write actions only after user confirmation; action = result returned + shown + saved; no sensitive personal data or finance;
KB docs are test instructions, never Avito regulations; line coverage > 80% per service.
Requirement IDs R01–R85 live in docs/context/requirements.md; scenario IDs S1–S4 in docs/context/product.md.
Every Scenario in a spec gets an ID like S3-05 that appears in the test name.
```

## 3. Example spec (first change: `agent-s3-absence-flow`)
File: `openspec/changes/agent-s3-absence-flow/specs/absence-request/spec.md`
```markdown
## ADDED Requirements

### Requirement: Day-off request requires explicit confirmation
The agent SHALL NOT call `absence.create_request` until the user has confirmed a draft shown as `action_proposal`. (R66, R71)

#### Scenario: S3-04 Draft before write
- **WHEN** an intern asks "хочу отгул в пятницу" and all slots are filled
- **THEN** the agent emits `action_proposal` with dates, type and approver
- **AND** no write tool is called in that turn

#### Scenario: S3-05 Confirmed request is created, shown and saved
- **WHEN** the intern confirms the proposal
- **THEN** `absence.create_request` is called with `idempotency_key = proposal_id` and a valid `confirmation_token`
- **AND** the agent emits `action_result` with the request id and status "На согласовании"
- **AND** an `actions` row linked to the plan task exists and is returned by `GET /api/actions` after reload

#### Scenario: S3-10 Service failure does not duplicate the request
- **WHEN** the absence service times out and the intern presses "Повторить"
- **THEN** at most one request exists for that proposal
- **AND** the UI shows a retryable error, then the result

### Requirement: Day-off flow respects rights
The agent SHALL create requests only for the authenticated user. (R71)

#### Scenario: S3-11 Request for another person is refused
- **WHEN** an intern asks "оформи отгул за Ивана"
- **THEN** the agent emits `refusal{reason: no_rights}` and calls no write tool
```
Test names then look like `test_S3_05_confirmed_request_created_shown_saved`.
