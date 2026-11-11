# attachments-websearch Specification

## Purpose
Files in chat and web search for general technical questions (organizers' Q&A 10.11: interns paste logs and screenshots; the main pain is engineering onboarding). Attached text is masked before use; screenshots and documents never leave the browser as content. Web search answers only general engineering questions outside the KB and marks results as external sources.

## Requirements

### Requirement: Attached logs, configs and code
The agent SHALL mask secrets in attached text before any processing and SHALL handle logs as troubleshooting, configs as a secrets check and code as review preparation.

#### Scenario: AT-01 Log file
- **WHEN** an intern attaches a log with a DB password and no text
- **THEN** the password never appears in events, the number of hidden secrets and the main error line are shown, and a draft to the mentor is offered

#### Scenario: AT-02 Config file
- **WHEN** an intern attaches an `.env` or YAML config
- **THEN** secrets are counted and hidden, the source is `eng-secrets` and the answer reminds about `.env.example` and `.gitignore`; a config without secrets is reported clean

#### Scenario: AT-03 Code file
- **WHEN** an intern attaches a source file
- **THEN** buddy prepares for code review instead of reviewing and warns about secrets found in the file

### Requirement: Screenshots and documents
The agent SHALL NOT guess the content of a screenshot and SHALL NOT read documents; it SHALL ask for the error text or say where documents are uploaded.

#### Scenario: AT-04 Screenshot
- **WHEN** an intern attaches a screenshot, with or without a question
- **THEN** buddy says it does not recognise screenshots and asks for the error text; a question in the same message is still answered

#### Scenario: AT-05 Document
- **WHEN** an intern attaches a PDF
- **THEN** buddy says it does not open documents and points to the HR portal, the curator or the team space (`common-documents-upload`)

#### Scenario: AT-06 Plain text file
- **WHEN** a text file without a known type is attached without a message
- **THEN** its masked text is routed like a message

#### Scenario: AT-07 Guardrails first
- **WHEN** the message text is a guarded request (prompt injection) with a file attached
- **THEN** the refusal is answered and the file is ignored

#### Scenario: AT-08 Bounds
- **WHEN** more than 3 files, an unknown kind or an empty message without files is sent
- **THEN** the request is rejected with 400

### Requirement: Web search for general technical questions
When the KB has no good answer to a general engineering question, the agent SHALL search public sources (`curated` list by default, `searxng` for live search, `off`), send only the masked question and show results as external sources, never as Avito rules.

#### Scenario: WS-01 External sources
- **WHEN** an intern asks how rebase differs from merge
- **THEN** sources have `source_type: external` and the badge «Внешний источник», the text says these are not Avito rules, no escalation draft is created

#### Scenario: WS-02 Company questions stay in the KB
- **WHEN** a question is about the team, HR or off-topic
- **THEN** no web search is made

#### Scenario: WS-03 Providers
- **WHEN** the provider is off, searxng without URL, searxng with a failing or valid instance, or curated without a list
- **THEN** off returns nothing, searxng returns at most `max_results` http(s) results and an error returns nothing

#### Scenario: WS-04 Secrets never leave
- **WHEN** a question contains a token
- **THEN** the query sent to the provider does not contain it
