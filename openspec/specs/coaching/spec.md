# coaching Specification

## Purpose
Coaching instead of doing (organizers' Q&A, 10.11): interns delegate work and paste LLM output without understanding it; the main onboarding pain is engineering. Buddy teaches, prepares for review, explains errors without leaking secrets, checks understanding and answers benefits without money operations.

## Requirements

### Requirement: Learn, don't delegate
The agent SHALL NOT do an intern's work task for them; it SHALL coach (steps, sources, when to ask the mentor) instead.

#### Scenario: CO-01 Delegation is coached
- **WHEN** an intern asks buddy to write the task code for them
- **THEN** the answer explains how to split the task into steps, cites `common-ai-usage`, names the mentor and creates no proposal

### Requirement: Code review preparation
The agent SHALL prepare the intern for code review with a self-check list, the team's rules and reviewers, and a draft to the mentor; it SHALL NOT review the code itself.

#### Scenario: CO-02 Reviewers and draft
- **WHEN** an intern asks whom to call for code review
- **THEN** sources are the team code-review document and `eng-branches-pr`, reviewers are named, an escalation draft to the mentor with topic code_review is emitted

#### Scenario: CO-03 Team-specific reviewers
- **WHEN** an intern of another team asks the same
- **THEN** only that team's reviewers and document are shown

### Requirement: Log troubleshooting with secret masking
The agent SHALL find the main error line in a pasted log, suggest a likely cause from the KB and SHALL mask secrets before echoing anything.

#### Scenario: CO-04 Known error
- **WHEN** an intern pastes a git `Permission denied (publickey)` log
- **THEN** the main error line is quoted, the hint points to Bitbucket SSH keys (`eng-repo-bitbucket`) and a draft to the mentor contains the log excerpt

#### Scenario: CO-05 Secrets masked
- **WHEN** the log contains a password, a DB URL with a password, a bearer token and a cloud key
- **THEN** none of the secrets appear in the answer or in the draft and the intern is told how many were hidden

#### Scenario: CO-06 Unknown error
- **WHEN** the log has no known pattern
- **THEN** generic advice and a draft are given, no source is invented

### Requirement: Knowledge check
The agent SHALL generate a short fill-in-the-blank quiz from an ACL-readable KB document, score the answer, and keep the quiz only until the next message.

#### Scenario: CO-07 Quiz from topic
- **WHEN** an intern asks «проверь меня по защите данных»
- **THEN** the quiz cites the document, has at least two blanks and the expected answers are kept in session state

#### Scenario: CO-08 Full score
- **WHEN** the intern answers all blanks correctly for a quiz built from an open plan task
- **THEN** the score is full and buddy suggests marking that plan task done (the intern does it, buddy does not)

#### Scenario: CO-09 Corrections
- **WHEN** the intern answers wrongly
- **THEN** each wrong blank shows the expected word and the intern's answer

#### Scenario: CO-10 Quiz ends
- **WHEN** the intern writes «стоп», starts another scenario, or triggers a guardrail
- **THEN** the quiz is closed and the message is handled normally; guardrails always win

#### Scenario: CO-11 No document
- **WHEN** no readable document fits
- **THEN** buddy asks for a topic

### Requirement: Benefits without money operations
The agent SHALL refuse benefit operations (reason out_of_scope, detail benefit) and SHALL still give the general reference with test figures; personal pay stays a sensitive-data refusal (R72).

#### Scenario: CO-12 Benefit operation
- **WHEN** an intern asks to connect a sports compensation
- **THEN** a refusal event is emitted, the benefits table with test figures is shown with its source, and a salary question is still refused as sensitive_data

### Requirement: Engineering onboarding and personal context
The agent SHALL answer engineering onboarding questions (repository, branches and PR, CI/CD, secrets, dev stand) from test KB documents with clean snippets, and SHALL use the user's office location to pick the right section.

#### Scenario: CO-13 Engineering answers
- **WHEN** an intern asks how to create a branch, how CI/CD works, where secrets live, how to start the dev stand, how to clone the repository
- **THEN** each answer cites the matching `eng-*` document with the test badge, without markdown headings, and a clone question is not turned into an access request

#### Scenario: CO-14 Location
- **WHEN** interns from Moscow and Saint Petersburg ask where to get a laptop
- **THEN** each gets their office section; an explicit city in the question wins over the profile

#### Scenario: CO-15 Help and roles
- **WHEN** an intern asks what buddy can do; an HR BP asks about code review
- **THEN** help lists the coaching abilities; code review is not available to the HR BP role

### Requirement: Fewer needless escalations
The agent SHALL understand the colloquial words interns type (ноут, репа, пр, впн, задеплоить) and, when no document answers the question, SHALL offer the closest documents before the draft to the mentor and SHALL say that the mentor's answer can be added to the knowledge base.

#### Scenario: CO-16 Colloquial words
- **WHEN** an intern asks «будет ли у меня рабочий ноут?», «как сделать пр в репу?» or «как настроить впн?»
- **THEN** the matching document answers with its source and no escalation; «стендап» and «квантовый компьютер» are not rewritten

#### Scenario: CO-17 Nearby documents and the knowledge loop
- **WHEN** no document answers the question
- **THEN** the closest readable documents are offered as «возможно, поможет», a draft to the mentor follows and the text explains that the answer can be added to the knowledge base
