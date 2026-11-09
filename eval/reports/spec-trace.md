# OpenSpec scenario → test trace

Scenarios: 140 · traced: 140

| capability | scenario | test files |
|---|---|---|
| absence-request | S3-01 Procedure with source | test_absence_s3.py |
| absence-request | S3-02 Relative dates | test_absence_s3.py, test_units.py |
| absence-request | S3-03 Slot filling across turns | test_absence_s3.py |
| absence-request | S3-17 Unparsed dates | test_absence_s3.py |
| absence-request | S3-04 Draft before write | test_absence_s3.py |
| absence-request | S3-08 Confirmation by text | test_absence_s3.py |
| absence-request | S3-09 Cancel | test_absence_s3.py |
| absence-request | S3-18 Proposal shape | test_absence_s3.py |
| absence-request | S3-05 Created, shown, saved | test_absence_s3.py |
| absence-request | S3-06 Remind about the mentor | test_absence_s3.py |
| absence-request | S3-10 Timeout without duplicates | test_absence_s3.py |
| absence-request | S3-10b Repeated confirm | test_absence_s3.py |
| absence-request | S3-12 Tampered or foreign confirmation | test_absence_s3.py |
| absence-request | S3-07 Weekend | test_absence_s3.py |
| absence-request | S3-07b Past, too long, overlap | test_absence_s3.py |
| absence-request | S3-13 Vacation and sick leave | test_absence_s3.py |
| absence-request | S3-14 Short notice | test_absence_s3.py |
| absence-request | S3-15 Scenario flag off | test_absence_s3.py |
| absence-request | S3-16 Role without absence tools | test_absence_s3.py |
| absence-request | S3-19 Not for today | test_absence_s3.py |
| absence-request | S3-11 For another person | test_absence_s3.py |
| access-and-channels | S4-01 List with statuses | test_access_s4.py |
| access-and-channels | S4-02 Ordinary access | test_access_s4.py |
| access-and-channels | S4-05 Already granted | test_access_s4.py |
| access-and-channels | S4-08 Unknown resource | test_access_s4.py |
| access-and-channels | S4-10 Team-specific lists | test_access_s4.py |
| access-and-channels | S4-12 All granted | test_access_s4.py |
| access-and-channels | S4-03 Elevated access | test_access_s4.py |
| access-and-channels | S4-06 Other team resource | test_access_s4.py |
| access-and-channels | S4-07 For another person | test_access_s4.py |
| access-and-channels | S4-09 Lead role | test_access_s4.py |
| access-and-channels | S4-04 Channels and draft | test_access_s4.py |
| access-and-channels | S4-11 Mentor view | test_access_s4.py |
| agent-behavior | AG-07 Exactly one terminal event | test_app_ag.py |
| agent-behavior | AG-16 Status stages stay in the contract enum | test_units.py |
| agent-behavior | AG-08 Request id is echoed | test_app_ag.py |
| agent-behavior | AG-02 Missing or invalid token | test_app_ag.py |
| agent-behavior | AG-03 Context mismatch | test_app_ag.py |
| agent-behavior | AG-13 Expired token | test_app_ag.py |
| agent-behavior | AG-15 Unknown role | test_app_ag.py |
| agent-behavior | AG-05 Service token | test_app_ag.py |
| agent-behavior | AG-04 Malformed bodies | test_app_ag.py |
| agent-behavior | AG-11 Internal error | test_app_ag.py |
| agent-behavior | AG-12 Tool failure is retryable | test_app_ag.py |
| agent-behavior | AG-01 Health, readiness, metrics | test_app_ag.py |
| agent-behavior | AG-06 Config per role | test_app_ag.py |
| agent-behavior | AG-18 Config validation | test_units.py |
| agent-behavior | AG-09 Clock override for demo | test_app_ag.py |
| agent-behavior | AG-10 FAQ draft from a mentor answer | test_app_ag.py |
| ask-buddy | S2-01 Answer with source and badge | test_qa_s2.py |
| ask-buddy | S2-02 Waiting statuses | test_qa_s2.py |
| ask-buddy | S2-03 Two teams, two answers | test_qa_s2.py |
| ask-buddy | S2-04 ACL before the LLM | test_qa_s2.py |
| ask-buddy | S2-07 Plan context | test_qa_s2.py |
| ask-buddy | S2-13 Alumni entry point | test_qa_s2.py |
| ask-buddy | S2-14 Document endpoint | test_qa_s2.py |
| ask-buddy | S2-15 ACL and snippets in retrieval | test_units.py |
| ask-buddy | S2-16 Non-test documents rejected | test_units.py |
| ask-buddy | S2-22 Documents without ACL rejected | test_units.py |
| ask-buddy | S2-17 Hybrid retrieval | test_units.py |
| ask-buddy | S2-05 No answer | test_qa_s2.py |
| ask-buddy | S2-06 Curator topics | test_qa_s2.py |
| ask-buddy | S2-12 Manual escalation uses history | test_qa_s2.py |
| ask-buddy | S2-08 Who is who | test_qa_s2.py |
| ask-buddy | S2-09 Human-only topics | test_qa_s2.py |
| ask-buddy | S2-10 Off-topic | test_qa_s2.py |
| ask-buddy | S2-11 Small talk and help | test_qa_s2.py |
| ask-buddy | S2-18 LLM answer | test_integrations.py |
| ask-buddy | S2-19 Fallback to stub | test_integrations.py |
| ask-buddy | S2-20 No fallback | test_integrations.py |
| ask-buddy | S2-21 Tool calling | test_integrations.py |
| nlu-and-llm | NLU-01 Train, save, load | test_training.py |
| nlu-and-llm | NLU-02 Training script and quality | test_training.py |
| nlu-and-llm | NLU-03 Cross-validation | test_training.py |
| nlu-and-llm | NLU-04 No model | test_training.py |
| nlu-and-llm | LLM-01 Stream and complete | test_integrations.py |
| nlu-and-llm | LLM-02 HTTP errors | test_integrations.py |
| nlu-and-llm | LLM-03 Timeouts | test_integrations.py |
| nlu-and-llm | LLM-04 Provider selection | test_integrations.py |
| nlu-and-llm | LLM-05 Embeddings | test_integrations.py |
| nlu-and-llm | MCP-11 Handshake and results | test_integrations.py |
| nlu-and-llm | MCP-12 Tool errors | test_integrations.py |
| nlu-and-llm | MCP-13 HTTP failures | test_integrations.py |
| nlu-and-llm | MCP-14 HTTP backend in the agent | test_integrations.py |
| nlu-and-llm | MCP-15 One base URL for B's mcp-mocks | test_integrations.py |
| plan-assistant | S1-01 Stage and next step | test_plan_s1.py |
| plan-assistant | S1-02 Overdue first | test_plan_s1.py |
| plan-assistant | S1-03 Status changes move the next step | test_plan_s1.py |
| plan-assistant | S1-04 Human-only step | test_plan_s1.py |
| plan-assistant | S1-05 Open tasks from core | test_plan_s1.py |
| plan-assistant | S1-06 Template fallback | test_plan_s1.py |
| plan-assistant | S1-07 Mentor plan | test_plan_s1.py |
| plan-assistant | S1-08 Alumni | test_plan_s1.py |
| plan-assistant | S1-09 Lead overview | test_plan_s1.py |
| plan-assistant | S1-14 Mentees unavailable | test_plan_s1.py |
| plan-assistant | S1-10 Equator summary | test_plan_s1.py |
| plan-assistant | S1-11 Mentor summary | test_plan_s1.py |
| plan-assistant | S1-12 Reminder texts | test_plan_s1.py |
| plan-assistant | S1-13 Summary endpoint | test_plan_s1.py |
| rights-guardrails | RG-01 Colleague absence | test_rights_rg.py |
| rights-guardrails | RG-02 Named person accesses | test_rights_rg.py |
| rights-guardrails | RG-20 Mentor sees only own mentees | test_rights_rg.py |
| rights-guardrails | RG-17 Lead decides own team requests | test_rights_rg.py |
| rights-guardrails | RG-18 Lead of another team | test_rights_rg.py |
| rights-guardrails | RG-03 Salary | test_rights_rg.py |
| rights-guardrails | RG-04 Salary of interns | test_rights_rg.py |
| rights-guardrails | RG-05 Medical data | test_rights_rg.py |
| rights-guardrails | RG-06 Hiring and dismissal | test_rights_rg.py |
| rights-guardrails | RG-07 Business trip operation | test_rights_rg.py |
| rights-guardrails | RG-11 Benefit operation | test_rights_rg.py |
| rights-guardrails | RG-12 Personal documents | test_rights_rg.py |
| rights-guardrails | RG-13 Redirect to HR BP | test_rights_rg.py |
| rights-guardrails | RG-08 Ignore instructions | test_rights_rg.py |
| rights-guardrails | RG-09 Tool name in a message | test_rights_rg.py |
| rights-guardrails | RG-10 Role play | test_rights_rg.py |
| rights-guardrails | RG-19 Injection inside a document | test_rights_rg.py |
| rights-guardrails | RG-14 Intern asks for lead tools | test_rights_rg.py |
| rights-guardrails | RG-15 Read tools per role | test_rights_rg.py |
| rights-guardrails | RG-16 Gateway blocks writes | test_rights_rg.py |
| rights-guardrails | MCP-01 Identity only from JWT | test_mocks.py |
| rights-guardrails | MCP-02 Foreign or missing confirmation | test_mocks.py |
| rights-guardrails | MCP-03 Idempotent write | test_mocks.py |
| rights-guardrails | MCP-04 Own records only | test_mocks.py |
| rights-guardrails | MCP-05 Lead of own team only | test_mocks.py |
| rights-guardrails | MCP-06 Elevated access creates an approval | test_mocks.py |
| rights-guardrails | MCP-07 KB search is ACL-filtered | test_mocks.py |
| rights-guardrails | MCP-08 Directory and checklist | test_mocks.py |
| rights-guardrails | MCP-09 Fault injection and adapters | test_mocks.py |
| rights-guardrails | MCP-10 Validation problems | test_mocks.py |
| rights-guardrails | CT-01 Canonical args hash | test_units.py |
| rights-guardrails | CT-02 Token verification matrix | test_units.py |
| rights-guardrails | CT-03 Malformed tokens | test_units.py |
| team-roles | TR-01 Pending list | test_team_tr.py |
| team-roles | TR-02 Approve | test_team_tr.py |
| team-roles | TR-03 Reject with comment | test_team_tr.py |
| team-roles | TR-04 Unknown id | test_team_tr.py |
| team-roles | TR-05 Nothing pending | test_team_tr.py |
| team-roles | TR-06 Elevated access approval | test_team_tr.py |
| team-roles | TR-07 Decision twice | test_team_tr.py |
| team-roles | TR-08 Lead sees team interns | test_team_tr.py |
