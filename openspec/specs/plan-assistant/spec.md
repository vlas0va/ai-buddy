# plan-assistant Specification

## Purpose
S1 in chat: stage, statuses, next step, role views, AI-cron reminder texts and the equator summary (R18–R20, R54, R58).

## Requirements

### Requirement: Next step from the plan
The agent SHALL compute the current stage, completed and overdue tasks and SHALL emit next_step for the most urgent open task.

#### Scenario: S1-01 Stage and next step
- **WHEN** an intern asks what to do next
- **THEN** the answer names the stage and next step and next_step carries a CTA message

#### Scenario: S1-02 Overdue first
- **WHEN** some tasks are overdue
- **THEN** overdue tasks are listed and the next step is overdue

#### Scenario: S1-03 Status changes move the next step
- **WHEN** the next task is marked done
- **THEN** another task becomes the next step

#### Scenario: S1-04 Human-only step
- **WHEN** the next task is human-only
- **THEN** next_step.human_only is true and the text says the intern does it with people

#### Scenario: S1-05 Open tasks from core
- **WHEN** core sends only open_tasks
- **THEN** the next step is computed from them

#### Scenario: S1-06 Template fallback
- **WHEN** no tasks are sent
- **THEN** the checklist template is used

### Requirement: Views by role
The agent SHALL give mentors their checklist items and mentee progress, leads an approvals overview and alumni an explanation without a plan.

#### Scenario: S1-07 Mentor plan
- **WHEN** a mentor asks for their plan
- **THEN** the answer includes their mentees

#### Scenario: S1-08 Alumni
- **WHEN** an alumni asks what to do next
- **THEN** the agent says the plan is over and stays an entry point

#### Scenario: S1-09 Lead overview
- **WHEN** a lead asks about the plan
- **THEN** the agent reports pending approvals

#### Scenario: S1-14 Mentees unavailable
- **WHEN** core sends no mentees
- **THEN** the agent points to the «Мои стажёры» screen

### Requirement: Reminders and summaries
The agent SHALL produce reminder texts for AI-cron and a progress summary for the equator without replacing the people who give feedback.

#### Scenario: S1-10 Equator summary
- **WHEN** an intern asks for a summary
- **THEN** done tasks and granted accesses are listed and the intern is asked to agree it with the mentor and curator

#### Scenario: S1-11 Mentor summary
- **WHEN** a mentor asks for mentee progress
- **THEN** a summary per mentee with the note that feedback is the mentor's

#### Scenario: S1-12 Reminder texts
- **WHEN** AI-cron sends items
- **THEN** human-only items get «помочь подготовиться», overdue and period-start items get their own texts

#### Scenario: S1-13 Summary endpoint
- **WHEN** core calls /v1/summary/progress
- **THEN** bullets and text are returned

### Requirement: Message to the mentor about a plan task
The «Спросить buddy» button of a plan task SHALL produce a draft to the mentor about that task (its open subtasks listed), not a knowledge-base search.

#### Scenario: S1-15 Draft about the task
- **WHEN** the intern sends «Помоги написать наставнику про задачу «…»» from the plan
- **THEN** the intent is task_message, a draft to the mentor names the task and lists its open subtasks, no sources and no «не нашёл ответа»

