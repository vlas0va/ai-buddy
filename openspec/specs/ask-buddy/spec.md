# ask-buddy Specification

## Purpose
S2 «Спроси buddy»: grounded answers about the team and tools with sources and the test badge, escalation to people, remind-not-replace (R02, R03, R05, R10, R15, R69).

## Requirements

### Requirement: Grounded answers with sources
The agent SHALL answer only from documents returned by kb.search and readable by the user, SHALL attach their sources with the test badge and SHALL never present them as Avito regulations.

#### Scenario: S2-01 Answer with source and badge
- **WHEN** an intern asks how code review works
- **THEN** sources contain delivery-code-review with source_type test and the badge; the text says «тестовая инструкция»

#### Scenario: S2-02 Waiting statuses
- **WHEN** a question is asked
- **THEN** status thinking then searching_kb precede the answer

#### Scenario: S2-03 Two teams, two answers
- **WHEN** interns of delivery and search ask the same question
- **THEN** they get their own team documents (R02)

#### Scenario: S2-04 ACL before the LLM
- **WHEN** an intern asks for the mentor handbook
- **THEN** mentor-only documents are never shown; a mentor gets them

#### Scenario: S2-07 Plan context
- **WHEN** the answer relates to an open plan task
- **THEN** the answer names that task (R15)

#### Scenario: S2-13 Alumni entry point
- **WHEN** an alumni asks a work question
- **THEN** the agent answers from the KB (R58)

#### Scenario: S2-14 Document endpoint
- **WHEN** a document is requested by id
- **THEN** readable documents are returned with the badge, others 404

#### Scenario: S2-15 ACL and snippets in retrieval
- **WHEN** the KB is searched
- **THEN** ACL filtering happens before ranking and only test documents are loaded

#### Scenario: S2-16 Non-test documents rejected
- **WHEN** a KB file has source_type other than test
- **THEN** loading fails

#### Scenario: S2-22 Documents without ACL rejected
- **WHEN** a KB file has no frontmatter or empty acl.roles or acl.teams
- **THEN** loading fails instead of silently hiding or exposing the document

#### Scenario: S2-17 Hybrid retrieval
- **WHEN** an embeddings endpoint is configured
- **THEN** BM25 and vector results are fused; a failing embedder falls back to BM25

### Requirement: Escalation when the KB has no answer
The agent SHALL say it found nothing and SHALL prepare an editable draft to the right person instead of guessing. The
intern sends it personally (direct message or the team channel): buddy does not deliver messages to the mentor
(organizers 13.11). An intern's question to the mentor, the lead or HR BP goes to that person's question stack, where it is
answered in a batch and the answer can be added to the knowledge base.

#### Scenario: S2-05 No answer
- **WHEN** the KB has no relevant document
- **THEN** an escalation_draft to the mentor is emitted and done.escalated is true

#### Scenario: S2-06 Curator topics
- **WHEN** the question is about university documents
- **THEN** the draft goes to the internship curator

#### Scenario: S2-23 Write the mentor directly, the question goes to the stack
- **WHEN** an intern's question has no answer in the KB
- **THEN** the agent names the mentor and the team channel, the escalation_draft carries `channel`, `queue: true` and the
  question, and the text says the question was added to the mentor's list; a mentor's or lead's draft has no `queue`

#### Scenario: S2-12 Manual escalation uses history
- **WHEN** the intern writes «спроси у наставника» after a question
- **THEN** the draft contains the previous question

#### Scenario: S2-08 Who is who
- **WHEN** the intern asks who their mentor is
- **THEN** the answer comes from the directory

### Requirement: Remind, do not replace the mentor
The agent SHALL NOT give feedback, run 1:1s, assess competencies or present at the equator; it SHALL remind and help prepare.

#### Scenario: S2-09 Human-only topics
- **WHEN** the intern asks for feedback
- **THEN** the agent explains that people do it and offers a draft request for a 1:1

#### Scenario: S2-10 Off-topic
- **WHEN** the intern asks about the weather
- **THEN** the agent politely declines

#### Scenario: S2-11 Small talk and help
- **WHEN** the intern greets, thanks or asks what buddy can do
- **THEN** short role-specific answers

### Requirement: Provider-independent answers
The agent SHALL produce the same events with the stub and with an OpenAI-compatible LLM, SHALL fall back to the stub on LLM failure when configured, and SHALL offer the LLM only read tools of the role.

#### Scenario: S2-18 LLM answer
- **WHEN** LLM_PROVIDER is openai_compat
- **THEN** tokens are streamed from the model, sources are the same and write scenarios stay deterministic

#### Scenario: S2-19 Fallback to stub
- **WHEN** the model fails and LLM_FALLBACK_TO_STUB is true
- **THEN** the answer is built by the stub and done.provider is stub_fallback

#### Scenario: S2-20 No fallback
- **WHEN** the model fails and fallback is off
- **THEN** error{retryable: true}

#### Scenario: S2-21 Tool calling
- **WHEN** LLM_TOOL_CALLING is true
- **THEN** only read tools of the role are offered and a call to a foreign tool is denied by the gateway

### Requirement: Mentor answers go to the knowledge base as a general FAQ (checkpoint 11.11)
An escalated question and the mentor's answer added to the knowledge base are valid for every intern: greetings, names
of people and concrete dates are removed before the FAQ is stored (core before kb.add_faq, the agent in faq-draft).

#### Scenario: S2-22 General FAQ
- **WHEN** a mentor answers «Анна, да, предупреди Марию за два дня, в пятницу согласует» with «в базу знаний»
- **THEN** the FAQ has no names and no dates («в нужный день»), and the answer form asks to write it in a general way

