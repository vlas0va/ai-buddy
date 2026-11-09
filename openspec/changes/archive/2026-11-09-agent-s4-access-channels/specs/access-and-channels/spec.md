## ADDED Requirements

### Requirement: Required accesses and requests
The agent SHALL list the accesses required in the user's team with statuses and SHALL prepare drafts only for missing ordinary accesses.

#### Scenario: S4-01 List with statuses
- **WHEN** an intern asks which accesses they need
- **THEN** each resource has a status, elevated ones are marked and drafts exist for jira, gitlab and the team space only

#### Scenario: S4-02 Ordinary access
- **WHEN** the intern requests Jira and confirms
- **THEN** the access is granted, the result is linked to checklist item CL-01 and shows as granted

#### Scenario: S4-05 Already granted
- **WHEN** the intern asks for Confluence they already have
- **THEN** no draft, the status is explained

#### Scenario: S4-08 Unknown resource
- **WHEN** the intern asks for a resource that is not in the team list
- **THEN** the agent lists the available resources

#### Scenario: S4-10 Team-specific lists
- **WHEN** interns of two teams ask
- **THEN** each sees only their team resources

#### Scenario: S4-12 All granted
- **WHEN** every ordinary access is granted
- **THEN** the agent says so

### Requirement: Elevated access requires manager approval
The agent SHALL create elevated access requests only as pending_approval for the team lead.

#### Scenario: S4-03 Elevated access
- **WHEN** the intern requests the closed incidents space and confirms
- **THEN** status pending_approval with the lead as approver and the lead sees it among pending approvals

### Requirement: Rights for access requests
The agent SHALL refuse requests for another person or another team's resources and SHALL keep access requests to intern and mentor roles.

#### Scenario: S4-06 Other team resource
- **WHEN** a delivery intern asks for the search team space
- **THEN** refusal no_rights, no draft

#### Scenario: S4-07 For another person
- **WHEN** an intern asks for Jira for Ivan
- **THEN** refusal no_rights

#### Scenario: S4-09 Lead role
- **WHEN** a lead asks which accesses they need
- **THEN** refusal explaining that leads approve requests

### Requirement: Channels and mentor visibility
The agent SHALL list team channels and prepare a draft asking the mentor to add the intern; the mentor SHALL see what the intern requested.

#### Scenario: S4-04 Channels and draft
- **WHEN** an intern asks about channels
- **THEN** channels with closed markers and an escalation_draft to the mentor listing them

#### Scenario: S4-11 Mentor view
- **WHEN** the mentor asks what Anna requested
- **THEN** the mentor sees Anna's access statuses from user_context.mentees
