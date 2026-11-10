## Why
The organizers' Q&A (10.11) named two real problems: interns delegate their tasks and use LLMs thoughtlessly, and the
hardest part of onboarding is engineering (Bitbucket repository, branches and PR, whom to call for code review, CI/CD and
GitOps, secrets, dev stand). Org questions need depth (laptop: where, when, which model). Benefits with test figures are
fine, money operations are not. Personal data buddy may use: location, name, chats.

## What Changes
- New intents: `learning`, `code_review`, `troubleshoot`, `quiz` (+ `quiz_answer` state), `benefit` (guardrail)
- Secret masking for pasted logs; error hints that point to KB documents
- 17 new test KB documents (`eng-*`, laptop, values, AI usage, network, documents, goals, library, analytics, frontend)
- GitLab → Bitbucket (resource id `bitbucket`) everywhere
- `user_context.location` (optional) picks the office section; snippets are rebuilt from the local KB copy without markdown
- Prompt rules 8–9 (coach, never echo secrets); help text lists the new abilities

## Capabilities
### New Capabilities
- `coaching`: 15 scenarios CO-01 … CO-15

### Modified Capabilities
- `rights-guardrails`: benefit operations are refused with a reference instead of a bare refusal (RG-11 unchanged)

## Out of scope
Reviewing code, running user code, web search.

## Impact
- Code: `services/agent/` (owner A): `agent/flows/coach.py`, router, qa, orchestrator, agent.yaml. Contract: optional
  `user_context.location` in `contracts/agent-stream.md`.
- Tests: tests/test_coaching_co.py, eval/cases/coaching.yaml (23 cases, `secrets` is a guard category).
