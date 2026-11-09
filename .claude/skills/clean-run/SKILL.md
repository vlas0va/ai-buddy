---
name: clean-run
description: Reproduce the expert's experience — reset everything, start strictly by README, run smoke e2e and walk the verification order. Use at the end of every phase and before submission; admission depends on it (R80).
---
# Clean run as the jury would do it

1. `docker compose --profile full down -v --remove-orphans` and remove local `.env` (keep `.env.example`).
2. Follow README "Быстрый старт" literally — copy each command from README, do not use shortcuts you know.
3. Confirm no LLM key is set: `LLM_PROVIDER` must default to `stub`.
4. Wait for healthchecks; record the time until the UI opens.
5. `make e2e 2>&1 | tail -30` — all declared scenarios green.
6. Walk README "Порядок проверки" manually with the test accounts; note every step where the text and the UI differ.
7. `make coverage | tail -15` — every service > 80%; report present in `coverage/`.
8. Write the protocol to `docs/clean-run/<date>.md`: commit hash, duration, failures, README fixes needed.
9. Any failure in steps 2–6 is a blocker for the phase gate.
