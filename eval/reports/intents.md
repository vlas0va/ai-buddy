# Intent classifier report

Train: 286 phrases, 14 intents · held-out test: 50 phrases · model: softmax logistic regression, 3686 features (stems + char 3–4-grams)

| metric | value |
|---|---|
| held-out test, classifier only | 90.0% |
| held-out test, full router (guardrails + rules + classifier) | **94.0%** |

## Router confusion on the held-out set

| intent | n | correct | routed instead |
|---|---|---|---|
| absence | 6 | 6 | — |
| absence_status | 3 | 3 | — |
| access | 4 | 4 | — |
| approvals | 3 | 3 | — |
| channels | 3 | 2 | kb_question (1) |
| escalate | 2 | 2 | — |
| help | 2 | 2 | — |
| kb_question | 10 | 10 | — |
| mentee | 2 | 2 | — |
| offtopic | 3 | 1 | kb_question (2) |
| plan | 4 | 4 | — |
| smalltalk | 3 | 3 | — |
| summary | 2 | 2 | — |
| whois | 3 | 3 | — |

Write scenarios (S3, S4, approvals) are matched by high-precision rules before the classifier, and refusals by deterministic guardrails, so classifier mistakes mostly land in `kb_question` (an answer from the knowledge base or an escalation draft) — never in a write action.
