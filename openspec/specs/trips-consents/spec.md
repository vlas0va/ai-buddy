# trips-consents Specification

## Purpose
Business trips and consents from the case «Пространство решений» (absences, trips, consents) and the organizers' Q&A of 10.11: buddy prepares a trip request to another Avito office for the lead's approval and shows the employee's consents, letting them sign voluntary ones. Tickets, hotels and money stay with the travel service and the HR BP (R72). Mock servers `trips` and `consents` (contracts/mcp-tools.md).

## Requirements

### Requirement: Trip procedure
The agent SHALL explain the trip procedure from `trips.get_policy` (offices, notice, length) and name the approver before collecting data.

#### Scenario: TP-01 Explain and ask
- **WHEN** an intern asks how to arrange a business trip
- **THEN** the answer gives the 5-day notice rule, the lead's name and asks for city, dates and purpose; no proposal is created

### Requirement: Trip request with confirmation
The agent SHALL collect city, dates and purpose (in one or several messages), validate them with `trips.validate_request` and show a draft; `trips.create_request` SHALL run only in a confirm turn.

#### Scenario: TP-02 One message to a request
- **WHEN** an intern writes the city, dates and purpose in one message and confirms the draft
- **THEN** the proposal has kind `trip_request` with exactly those args, no write runs before confirmation, and the result is `TRP-…` with status `pending_approval`, visible in «мои заявки»

#### Scenario: TP-03 Slot filling
- **WHEN** the intern gives the city, the dates and the purpose in separate messages
- **THEN** buddy asks only for what is missing and the follow-ups are routed by dialogue state

#### Scenario: TP-04 Validation
- **WHEN** the city is the intern's own office, the start is less than 5 days ahead, the trip is longer than 14 days or overlaps another trip
- **THEN** buddy explains the problem and creates no draft

### Requirement: No booking or payments
The agent SHALL NOT book tickets or hotels or discuss money for trips; it SHALL say the travel service books after approval.

#### Scenario: TP-05 Booking refused
- **WHEN** an intern asks to book a ticket or a hotel
- **THEN** the answer is a refusal with detail `trip` that explains who books; a cancelled draft is dropped and «да» does not replace the button

### Requirement: Lead approves trips
The lead SHALL see trip requests among pending approvals and decide them in chat or in «Согласования».

#### Scenario: TP-06 Lead decision
- **WHEN** the lead approves the intern's `TRP-…` in chat after confirmation
- **THEN** the trip status becomes «Согласовано» for the intern

#### Scenario: TP-07 Roles
- **WHEN** an HR BP asks to arrange a trip, or an intern without trips asks for their status
- **THEN** the HR BP gets a `no_rights` refusal and the intern is told there are no trip requests

### Requirement: Consents
The agent SHALL show the employee's consents with statuses from `consents.list`; voluntary consents SHALL be signed only after confirmation (`consents.sign`); consents signed in the HR portal SHALL NOT be signed through buddy; revoking goes through the HR BP.

#### Scenario: CN-01 List
- **WHEN** an intern asks which consents they have
- **THEN** each consent is listed as mandatory or voluntary with its status and where it was signed, with a hint how to sign a voluntary one

#### Scenario: CN-02 Sign voluntary
- **WHEN** an intern asks to sign the photo consent and confirms
- **THEN** the proposal has kind `consent` and args `{consent_id: photo}`, the result is `CNS-…` with status `signed`; asking again says it is already signed

#### Scenario: CN-03 HR portal consents and revoke
- **WHEN** an intern asks to sign the personal data or NDA consent, or to revoke a consent
- **THEN** no proposal is created: buddy says it is signed in the HR portal (or already signed), and revoking is sent to the HR BP by name

#### Scenario: CN-04 All signed
- **WHEN** all voluntary consents are signed
- **THEN** the list says all consents are signed
