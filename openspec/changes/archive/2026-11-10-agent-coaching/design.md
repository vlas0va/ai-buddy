## Design
- Deterministic handlers (ADR-007): coaching answers are templates over ACL-readable KB documents; no LLM call is needed,
  so the stub provider covers everything and nothing user-pasted reaches the LLM unmasked.
- Router order: guardrails (sensitive → benefit ops → injection → human-only → delegation → log detector) → rules
  (`quiz`, `code_review` before `access`) → classifier (4 new classes trained on 52 phrases) → kb_question.
- Quiz: up to 3 sentences of 40–220 chars from the document, the number or the longest non-stopword is blanked; answers are
  compared by Snowball stem; state lives in the session store and is dropped by any guardrail or scenario intent.
- Secret masking: key=value secrets, bearer tokens, AWS-style keys, JWTs, DB URLs with passwords.
