# access-and-channels Specification

## Purpose
S4 «Доступы и каналы»: required accesses with statuses, ordinary and elevated requests, team channels and a draft to the mentor (CL-01, CL-04, CL-05).

## Requirements

### Requirement: Required accesses and requests
The agent SHALL list the accesses required in the user's team with statuses and SHALL prepare drafts only for missing ordinary accesses.

#### Scenario: S4-01 List with statuses
- **WHEN** an intern asks which accesses they need
- **THEN** each resource has a status, elevated ones are marked and drafts exist for jira, bitbucket and the team space only

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

#### Scenario: S4-04 Channels and advice
- **WHEN** an intern asks about channels
- **THEN** channels with closed markers and advice: join open ones yourself, ask the mentor (by name) in a personal message for closed ones; no draft (organizers' feedback: chats are asked for in personal messages)

#### Scenario: S4-11 Mentor view
- **WHEN** the mentor asks what Anna requested
- **THEN** the mentor sees Anna's access statuses from user_context.mentees

### Requirement: Concrete resource, environment and rights (checkpoint 11.11)
An access request names a concrete resource (a Jira project, a Bitbucket repository, a database), the environment
(dev, stage, prod — only for resources that have environments) and the rights (чтение, запись, администрирование).
Reading dev/stage is granted at once; write, admin and anything on prod are approved by the lead; the resource says who
approves. The list of accesses is a table.

#### Scenario: S4-13 Jira project and repository
- **WHEN** the intern asks for the Jira project DLV, then for write rights to the repository delivery-api
- **THEN** the project is requested on read and granted; the repository draft has level write, requires approval and
  names the repository owner as approver

#### Scenario: S4-14 Database with environment and rights
- **WHEN** the intern asks for the database on stage on read, then on prod
- **THEN** stage read is granted at once and shown as «(stage, чтение)»; prod requires approval (DevOps gives the rights)

#### Scenario: S4-15 Database without environment
- **WHEN** the environment is not named for a database
- **THEN** the draft takes dev and says how to ask for another environment

#### Scenario: S4-16 Unavailable rights
- **WHEN** the rights are not offered by the resource (admin in Confluence)
- **THEN** no proposal, the available rights are listed

#### Scenario: S4-17 Environment for a resource without environments
- **WHEN** an environment is named for a Jira project
- **THEN** the draft has no environment and buddy says the resource has none

