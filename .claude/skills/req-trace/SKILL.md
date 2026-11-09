---
name: req-trace
description: Trace every case requirement R01–R85 to evidence in the repo and update statuses. Use daily, before declaring a scenario, and before submission.
---
# Requirement traceability

1. Delegate the scan to the `req-auditor` subagent (Haiku, read-only) — it returns only the requirements without evidence.
2. For each gap decide: fix now, assign (owner from the table), or drop the related claim from README/presentation.
   A scenario is declared in README only if its e2e test is green (R65).
3. Update the "Статус" column in `docs/context/requirements.md` (`[ ]` → `[~]` → `[x]`; `[v]` only after a clean-run).
4. Post a 5-line summary for the daily sync: covered N/85, blockers, owner per blocker.
