-- Core schema (owner B). Runs once on first Postgres start via /docker-entrypoint-initdb.d.
-- Identity comes from the mock IdP (core.users); all data is synthetic (R68, R70).

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE SCHEMA IF NOT EXISTS core;

-- Mock IdP store. Passwords are bcrypt hashes of test passwords listed in README (R34).
CREATE TABLE core.users (
    id            text PRIMARY KEY,              -- = login, e.g. intern.anna
    display_name  text NOT NULL,
    role          text NOT NULL CHECK (role IN ('intern','mentor','lead','hrbp','alumni','curator')),
    team_id       text NOT NULL,
    cohort_start  date,
    password_hash text NOT NULL,
    can_login     boolean NOT NULL DEFAULT true
);

CREATE TABLE core.plans (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      text NOT NULL REFERENCES core.users(id),
    plan_role    text NOT NULL CHECK (plan_role IN ('intern','mentor')),
    intern_id    text REFERENCES core.users(id),  -- for a mentor plan: whose onboarding it supports
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, plan_role, intern_id)
);

CREATE TABLE core.plan_tasks (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id           uuid NOT NULL REFERENCES core.plans(id) ON DELETE CASCADE,
    parent_task_id    uuid REFERENCES core.plan_tasks(id) ON DELETE CASCADE,
    source            text NOT NULL DEFAULT 'checklist' CHECK (source IN ('checklist','action')),
    checklist_item_id text,                      -- CL-01..CL-16; NULL for source = 'action' (S3 day off, R66)
    period            text NOT NULL CHECK (period IN ('week_1','week_2','month_1','month_2','after_3_months')),
    position          int  NOT NULL DEFAULT 0,
    title             text NOT NULL,
    description       text,
    status            text NOT NULL DEFAULT 'not_started'
                      CHECK (status IN ('not_started','in_progress','waiting_approval','done','overdue')),
    due_at            date,
    human_only        boolean NOT NULL DEFAULT false,
    linked_action_id  uuid,
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (source <> 'checklist' OR checklist_item_id IS NOT NULL)
);
CREATE INDEX plan_tasks_plan_idx ON core.plan_tasks (plan_id, period, position);

CREATE TABLE core.goals (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id    uuid NOT NULL REFERENCES core.plans(id) ON DELETE CASCADE,
    title      text NOT NULL,
    criteria   text,
    created_by text NOT NULL REFERENCES core.users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.chat_sessions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    text NOT NULL REFERENCES core.users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.chat_messages (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES core.chat_sessions(id) ON DELETE CASCADE,
    author     text NOT NULL CHECK (author IN ('user','assistant')),
    text       text NOT NULL,
    sources    jsonb NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX chat_messages_session_idx ON core.chat_messages (session_id, created_at);

-- Write proposals waiting for user confirmation (R66, R71).
CREATE TABLE core.proposals (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  uuid NOT NULL REFERENCES core.chat_sessions(id) ON DELETE CASCADE,
    user_id     text NOT NULL REFERENCES core.users(id),
    kind        text NOT NULL CHECK (kind IN ('absence_request','access_request')),
    draft       jsonb NOT NULL,
    draft_hash  text NOT NULL,                   -- sha256(canonical_json(draft.args)), see contracts/mcp-tools.md
    status      text NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','confirmed','executed','cancelled','expired','failed')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL DEFAULT now() + interval '30 minutes'
);

-- Executed actions: result returned by the mock service, shown and saved (R66).
CREATE TABLE core.actions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          text NOT NULL REFERENCES core.users(id),
    kind             text NOT NULL CHECK (kind IN ('absence_request','access_request')),
    external_id      text,                       -- ABS-000123 / ACC-000045
    status           text NOT NULL CHECK (status IN ('pending_approval','approved','rejected','granted','created','failed')),
    summary          text NOT NULL,
    payload          jsonb NOT NULL DEFAULT '{}',
    idempotency_key  uuid NOT NULL UNIQUE,       -- = proposal id (R79)
    task_id          uuid REFERENCES core.plan_tasks(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX actions_user_idx ON core.actions (user_id, created_at DESC);
ALTER TABLE core.plan_tasks
    ADD CONSTRAINT plan_tasks_action_fk FOREIGN KEY (linked_action_id) REFERENCES core.actions(id) ON DELETE SET NULL;

CREATE TABLE core.escalations (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    from_user_id text NOT NULL REFERENCES core.users(id),
    to_user_id   text NOT NULL REFERENCES core.users(id),
    to_role      text NOT NULL CHECK (to_role IN ('mentor','lead','hrbp','curator')),
    session_id   uuid REFERENCES core.chat_sessions(id) ON DELETE SET NULL,
    text         text NOT NULL,
    status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open','answered')),
    answer       text,
    added_to_kb  boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    answered_at  timestamptz
);

CREATE TABLE core.notifications (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           text NOT NULL REFERENCES core.users(id),
    kind              text NOT NULL CHECK (kind IN ('reminder','overdue','status_change','escalation_answer')),
    title             text NOT NULL,
    body              text,
    checklist_item_id text,
    dedupe_key        text,                      -- AI-cron: one reminder per (user, item, kind, period)
    read_at           timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user_idx ON core.notifications (user_id, created_at DESC);
CREATE UNIQUE INDEX notifications_dedupe_idx ON core.notifications (user_id, dedupe_key) WHERE dedupe_key IS NOT NULL;

CREATE TABLE core.feedback (
    message_id uuid NOT NULL REFERENCES core.chat_messages(id) ON DELETE CASCADE,
    user_id    text NOT NULL REFERENCES core.users(id),
    useful     boolean NOT NULL,
    comment    text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id)
);

-- Runtime feature flags (R55); empty teams = all teams.
CREATE TABLE core.feature_flags (
    key        text PRIMARY KEY,
    enabled    boolean NOT NULL,
    teams      text[] NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO core.feature_flags (key, enabled) VALUES
    ('scenario_s1', true), ('scenario_s2', true), ('scenario_s3', true), ('scenario_s4', true),
    ('proactive_digest', true), ('escalation_to_kb', false);

-- Analytics (contracts/events.md, R24) and transactional outbox for Kafka (R62).
CREATE TABLE core.analytics_events (
    event_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ts       timestamptz NOT NULL DEFAULT now(),
    name     text NOT NULL,
    user_id  text,
    role     text,
    team_id  text,
    props    jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX analytics_events_name_ts_idx ON core.analytics_events (name, ts);

CREATE TABLE core.outbox (
    id           bigserial PRIMARY KEY,
    topic        text NOT NULL CHECK (topic IN ('buddy.domain','buddy.clickstream')),
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX outbox_unpublished_idx ON core.outbox (id) WHERE published_at IS NULL;
