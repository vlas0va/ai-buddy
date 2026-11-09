# Agent coverage summary

Reproduce: `make coverage` (root) or `cd services/agent && make coverage` → HTML and Cobertura in `coverage/agent/`.
Counted: `services/agent/agent` and `training`; excluded: tests and the trained model JSON (R73).

```
Name                           Stmts   Miss  Cover   Missing
------------------------------------------------------------
agent/__init__.py                  1      0   100%
agent/app.py                     136      0   100%
agent/auth.py                     63      0   100%
agent/canonical.py                50      0   100%
agent/clock.py                    13      0   100%
agent/config.py                  101      1    99%   44
agent/dates.py                   133      4    97%   161, 174-175, 203
agent/dev.py                      92      0   100%
agent/errors.py                   30      0   100%
agent/flows/__init__.py            0      0   100%
agent/flows/absence.py            98      0   100%
agent/flows/access.py             96      1    99%   160
agent/flows/common.py             86      8    91%   80-81, 89-92, 141, 166
agent/flows/plan.py              131     10    92%   41-42, 67, 117-118, 131, 168-169, 184-185
agent/flows/qa.py                147     12    92%   33, 35, 37, 44, 70, 101-102, 139, 157-158, 234, 273
agent/flows/team.py               76      0   100%
agent/llm/__init__.py              0      0   100%
agent/llm/openai_compat.py        82      0   100%
agent/llm/writer.py               82      3    96%   85-86, 148
agent/metrics.py                  34      0   100%
agent/nlu/__init__.py              0      0   100%
agent/nlu/classifier.py           95      0   100%
agent/nlu/router.py               86      3    97%   174, 216, 222
agent/orchestrator.py            215     15    93%   117-121, 127-129, 191, 195-196, 277, 280, 282, 306-307, 363
agent/retrieval.py               168      7    96%   42, 65, 181, 215, 225-226, 243
agent/sse.py                      35      0   100%
agent/state.py                    51      1    98%   66
agent/text.py                     37      0   100%
agent/tools/__init__.py            0      0   100%
agent/tools/base.py               33      0   100%
agent/tools/inprocess.py         262      2    99%   340, 402
agent/tools/mcp_client.py         94      7    93%   50, 60-61, 79, 123-124, 140
training/__init__.py               0      0   100%
training/augment_with_llm.py      36      0   100%
training/train_intents.py         67      3    96%   81-88
------------------------------------------------------------
TOTAL                           2630     77    97%
```
