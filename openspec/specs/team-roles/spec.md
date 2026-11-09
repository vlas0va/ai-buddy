# team-roles Specification

## Purpose
Mentor and lead scenarios in the same interface: approvals in chat and mentee progress (R57, R67).

## Requirements

### Requirement: Lead approvals in chat
The agent SHALL show a lead the requests waiting for their decision and SHALL prepare approve or reject drafts executed only after confirmation.

#### Scenario: TR-01 Pending list
- **WHEN** an intern created a request and the lead asks what waits for approval
- **THEN** the request id and requester are listed

#### Scenario: TR-02 Approve
- **WHEN** the lead approves the request and confirms
- **THEN** status approved and the intern sees «Согласовано»

#### Scenario: TR-03 Reject with comment
- **WHEN** the lead rejects with «потому что …»
- **THEN** decision rejected with the comment, the intern sees «Отклонено»

#### Scenario: TR-04 Unknown id
- **WHEN** the id is not pending for the lead
- **THEN** the agent says so

#### Scenario: TR-05 Nothing pending
- **WHEN** no requests wait
- **THEN** the agent says there are none

#### Scenario: TR-06 Elevated access approval
- **WHEN** the lead approves an elevated access
- **THEN** the intern sees the access as granted

#### Scenario: TR-07 Decision twice
- **WHEN** the same request is decided twice
- **THEN** the second confirmation fails with a non-retryable error

### Requirement: Mentee progress
The agent SHALL show mentors and leads the progress of their own interns only.

#### Scenario: TR-08 Lead sees team interns
- **WHEN** a lead asks about their intern
- **THEN** the interns of the lead's mentors are shown
