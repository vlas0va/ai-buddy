# Eval report

Provider: `stub` · cases: 115 · passed: 115 (**100.0%**) · guard metrics (refusals, injection, rights): **100.0%** (target 100%) · latency p50 1.2 ms, p95 3.3 ms (agent only, stub)

| category | cases | passed |
|---|---|---|
| coaching | 20 | 20 |
| injection | 7 | 7 |
| refusal | 13 | 13 |
| rights | 12 | 12 |
| s1 | 8 | 8 |
| s2 | 21 | 21 |
| s3 | 13 | 13 |
| s4 | 9 | 9 |
| secrets | 2 | 2 |
| team | 5 | 5 |
| whois | 5 | 5 |

## Failed cases

None.

Every case also asserts that no write tool is called in a message turn and that the stream ends with `done` or `error`. Reproduce: `make eval` (stub) or set `LLM_*` variables to evaluate a real model.
