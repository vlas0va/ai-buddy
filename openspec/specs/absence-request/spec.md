# absence-request Specification

## Purpose
S3 «Оформить отгул» — the admission scenario: explain the procedure, collect and validate dates, show a draft, write only after confirmation, show and save the result (R04, R17, R66, R79).

## Requirements

### Requirement: Explain and collect dates
The agent SHALL explain the day-off procedure from the test instruction with its source, SHALL parse Russian date expressions and SHALL keep slots across turns.

#### Scenario: S3-01 Procedure with source
- **WHEN** an intern asks how to take a day off
- **THEN** the agent cites absence-policy with the test badge, names the approver and asks for dates

#### Scenario: S3-02 Relative dates
- **WHEN** an intern says «хочу отгул в пятницу»
- **THEN** dates resolve to the coming Friday and are validated by absence.validate_request

#### Scenario: S3-03 Slot filling across turns
- **WHEN** the intern first asks for a day off and then gives «с 16 по 17 ноября»
- **THEN** the draft covers both days and is routed by dialogue state

#### Scenario: S3-17 Unparsed dates
- **WHEN** the answer contains no date
- **THEN** the agent asks again with examples

### Requirement: Day-off request requires explicit confirmation
The agent SHALL NOT call absence.create_request until the user has confirmed a draft shown as action_proposal.

#### Scenario: S3-04 Draft before write
- **WHEN** an intern asks for a day off and all slots are filled
- **THEN** the agent emits action_proposal with args and approver and no write tool is called in that turn

#### Scenario: S3-08 Confirmation by text
- **WHEN** the intern types «да, отправляй»
- **THEN** the agent asks to press «Отправить» and creates nothing

#### Scenario: S3-09 Cancel
- **WHEN** the intern writes «отмена»
- **THEN** the draft is discarded

#### Scenario: S3-18 Proposal shape
- **WHEN** a proposal is emitted
- **THEN** it has proposal_id (uuid), kind, summary and draft{tool, args, display}

### Requirement: Confirmed request is created, shown and saved
The agent SHALL execute the confirmed draft unchanged with idempotency_key = proposal_id and SHALL return the service result for the card.

#### Scenario: S3-05 Created, shown, saved
- **WHEN** the intern confirms the proposal
- **THEN** tool_result carries ABS id, pending_approval, «На согласовании» and the approver; the request is listed after reload

#### Scenario: S3-06 Remind about the mentor
- **WHEN** a draft or result is shown
- **THEN** the text reminds to warn the mentor without replacing personal talk

#### Scenario: S3-10 Timeout without duplicates
- **WHEN** the service times out after committing and the intern presses «Повторить»
- **THEN** a retryable error is shown, then the same request with replayed: true; one record exists

#### Scenario: S3-10b Repeated confirm
- **WHEN** the same proposal is confirmed twice
- **THEN** the cached result is returned with replayed: true

#### Scenario: S3-12 Tampered or foreign confirmation
- **WHEN** the token is invalid, the args were changed or another user confirms
- **THEN** error confirmation_invalid and nothing is written

### Requirement: Validation and scope
The agent SHALL relay validation problems, SHALL warn about short notice and SHALL create only day_off requests for roles allowed by the absence tools.

#### Scenario: S3-07 Weekend
- **WHEN** the date is a Saturday
- **THEN** no proposal, the reason «выходной» is shown

#### Scenario: S3-07b Past, too long, overlap
- **WHEN** the date is past, longer than 3 working days or overlaps an existing request
- **THEN** no proposal and the problem is explained

#### Scenario: S3-13 Vacation and sick leave
- **WHEN** the intern asks for a vacation with dates
- **THEN** the draft is a request of type vacation, not a day off
- **WHEN** the intern reports a sick leave
- **THEN** no proposal: medical data stays out of the chat, the lead and HR BP are named

#### Scenario: S3-14 Short notice
- **WHEN** the day off is tomorrow
- **THEN** the draft warns to inform the lead personally

#### Scenario: S3-15 Scenario flag off
- **WHEN** the runtime flag scenario_s3 is false for the team
- **THEN** the agent says the pilot scenario is off

#### Scenario: S3-16 Role without absence tools
- **WHEN** an alumni asks for a day off
- **THEN** refusal no_rights

#### Scenario: S3-19 Not for today
- **WHEN** the intern asks for a day off today
- **THEN** the service rejects it (requests start tomorrow) and the agent advises to tell the lead personally

#### Scenario: S3-11 For another person
- **WHEN** an intern asks to file a day off for Ivan
- **THEN** refusal no_rights and no proposal

### Requirement: One absence request with a type, documents and two approval stages (checkpoint 11.11)
The request has dates, a type (day_off, vacation, absence, parental_leave) and up to 3 documents attached to the chat
message (metadata only). The lead approves first, then the HR admin; until the HR admin approves, the dates can be moved.

#### Scenario: S3-23 Vacation question
- **WHEN** the intern asks how many vacation days there are and how to take a vacation
- **THEN** the answer gives the Labour Code facts (28 calendar days, art. 115; after 6 months, art. 122), the process
  (dates → type → documents → lead → HR admin, reschedule until the HR admin approves) and asks for dates; no proposal

#### Scenario: S3-24 Vacation request
- **WHEN** the intern asks for a vacation with dates at least 14 days ahead
- **THEN** the draft has type vacation, calendar days, and the created request is shown as «Отпуск …»

#### Scenario: S3-25 Vacation notice
- **WHEN** the vacation starts in less than 14 days
- **THEN** no proposal, the notice rule is explained

#### Scenario: S3-26 Parental leave needs a document
- **WHEN** the intern asks for parental leave without a document
- **THEN** no proposal, buddy asks to attach a document
- **WHEN** the intern then attaches a document
- **THEN** the draft carries the document metadata and the created request keeps it

#### Scenario: S3-27 Absence with a document
- **WHEN** the intern asks for an absence for study and attaches a document in the same message
- **THEN** the draft has type absence and lists the document

#### Scenario: S3-28 Lead then HR admin
- **WHEN** the lead approves the request
- **THEN** it waits for the HR admin («Ждёт HR-администратора»), the HR admin sees it in the queue with the stage
- **WHEN** the HR admin approves
- **THEN** the request is approved and the steps show the HR admin registered it

#### Scenario: S3-29 Reschedule until the HR admin approves
- **WHEN** the intern asks to move a request the HR admin has not approved
- **THEN** a draft absence_reschedule (tool absence.reschedule_request) with the new dates; after confirmation the request
  goes back to the lead
- **WHEN** the request is already approved by the HR admin, unknown, or the new dates are invalid
- **THEN** no proposal and the reason is explained

