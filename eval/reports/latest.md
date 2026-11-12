# Eval report

Provider: `stub` · cases: 139 · passed: 139 (**100.0%**) · guard metrics (refusals, injection, rights): **100.0%** (target 100%) · latency p50 1.2 ms, p95 3.4 ms (agent only, stub)

| category | cases | passed |
|---|---|---|
| coaching | 23 | 23 |
| consents | 4 | 4 |
| injection | 7 | 7 |
| refusal | 15 | 15 |
| rights | 12 | 12 |
| s1 | 8 | 8 |
| s2 | 21 | 21 |
| s3 | 16 | 16 |
| s4 | 11 | 11 |
| secrets | 2 | 2 |
| team | 5 | 5 |
| trips | 7 | 7 |
| websearch | 3 | 3 |
| whois | 5 | 5 |

## Failed cases

None.

Every case also asserts that no write tool is called in a message turn and that the stream ends with `done` or `error`. Reproduce: `make eval` (stub) or set `LLM_*` variables to evaluate a real model.
