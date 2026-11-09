-- Schemas of mock Avito services behind MCP (owner B, R39, R52, R56). One schema per server.
-- Synthetic data only (R68). These imitate existing systems; they are not HR systems of our own (R70).

CREATE SCHEMA IF NOT EXISTS directory;
CREATE SCHEMA IF NOT EXISTS checklist;
CREATE SCHEMA IF NOT EXISTS absence;
CREATE SCHEMA IF NOT EXISTS approvals;
CREATE SCHEMA IF NOT EXISTS access;
CREATE SCHEMA IF NOT EXISTS kb;

-- directory: employee data imitation
CREATE TABLE directory.teams (
    id   text PRIMARY KEY,
    name text NOT NULL
);
CREATE TABLE directory.employees (
    id           text PRIMARY KEY,
    display_name text NOT NULL,
    role         text NOT NULL CHECK (role IN ('intern','mentor','lead','hrbp','alumni','curator')),
    team_id      text NOT NULL REFERENCES directory.teams(id),
    cohort_start date,
    mentor_id    text REFERENCES directory.employees(id),
    lead_id      text REFERENCES directory.employees(id),
    hrbp_id      text REFERENCES directory.employees(id),
    curator_id   text REFERENCES directory.employees(id),
    channel      text                            -- work chat name, no personal contacts
);

-- checklist: Avito intern checklist (data/checklist/checklist.yaml)
CREATE TABLE checklist.items (
    id          text PRIMARY KEY CHECK (id ~ '^CL-[0-9]{2}$'),
    role        text NOT NULL CHECK (role IN ('intern','mentor','both')),
    period      text NOT NULL CHECK (period IN ('week_1','week_2','month_1','month_2','after_3_months')),
    position    int  NOT NULL,
    title       text NOT NULL,
    description text,
    steps       jsonb NOT NULL DEFAULT '[]',
    scenarios   text[] NOT NULL,
    buddy       text,
    human_only  boolean NOT NULL
);

-- absence: absence system imitation (S3)
CREATE SEQUENCE absence.request_seq START 100;
CREATE TABLE absence.requests (
    id              text PRIMARY KEY DEFAULT 'ABS-' || lpad(nextval('absence.request_seq')::text, 6, '0'),
    employee_id     text NOT NULL REFERENCES directory.employees(id),
    type            text NOT NULL CHECK (type IN ('day_off')),
    start_date      date NOT NULL,
    end_date        date NOT NULL CHECK (end_date >= start_date),
    comment         text,
    status          text NOT NULL DEFAULT 'pending_approval'
                    CHECK (status IN ('pending_approval','approved','rejected','cancelled')),
    approver_id     text NOT NULL REFERENCES directory.employees(id),
    idempotency_key uuid NOT NULL UNIQUE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    decided_at      timestamptz
);
CREATE INDEX absence_requests_employee_idx ON absence.requests (employee_id, start_date);

-- access: access and channels imitation (S4)
CREATE TABLE access.resources (
    id                text PRIMARY KEY,
    name              text NOT NULL,
    kind              text NOT NULL CHECK (kind IN ('tool','space','channel')),
    team_id           text REFERENCES directory.teams(id),  -- NULL = required for every team
    elevated          boolean NOT NULL DEFAULT false,
    purpose           text,
    checklist_item_id text
);
CREATE SEQUENCE access.request_seq START 40;
CREATE TABLE access.requests (
    id              text PRIMARY KEY DEFAULT 'ACC-' || lpad(nextval('access.request_seq')::text, 6, '0'),
    employee_id     text NOT NULL REFERENCES directory.employees(id),
    resource_id     text NOT NULL REFERENCES access.resources(id),
    reason          text,
    status          text NOT NULL CHECK (status IN ('pending_approval','granted','rejected')),
    approver_id     text REFERENCES directory.employees(id),
    idempotency_key uuid NOT NULL UNIQUE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    decided_at      timestamptz
);

-- approvals: approval system imitation ("Согласия"), shared by S3 and S4
CREATE TABLE approvals.items (
    id              text PRIMARY KEY DEFAULT 'APR-' || substr(md5(gen_random_uuid()::text), 1, 8),
    kind            text NOT NULL CHECK (kind IN ('absence_request','access_request')),
    ref_id          text NOT NULL,              -- ABS-... or ACC-...
    requester_id    text NOT NULL REFERENCES directory.employees(id),
    approver_id     text NOT NULL REFERENCES directory.employees(id),
    summary         text NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    comment         text,
    idempotency_key uuid UNIQUE,                -- of the decide call
    created_at      timestamptz NOT NULL DEFAULT now(),
    decided_at      timestamptz,
    UNIQUE (kind, ref_id)
);
CREATE INDEX approvals_approver_idx ON approvals.items (approver_id, status);

-- kb: knowledge base with ACL applied before anything reaches the LLM (R71) and Russian full-text search
CREATE TABLE kb.documents (
    doc_id      text PRIMARY KEY,
    title       text NOT NULL,
    body        text NOT NULL,
    source_type text NOT NULL DEFAULT 'test' CHECK (source_type = 'test'),  -- R69
    acl_roles   text[] NOT NULL,                -- e.g. {intern,mentor,lead}
    acl_teams   text[] NOT NULL,                -- team ids or {*}
    owner_id    text REFERENCES directory.employees(id),  -- set only for private notes (CL-04 step 3)
    checklist_item_id text,
    url         text,
    updated     date NOT NULL DEFAULT current_date,
    tsv         tsvector GENERATED ALWAYS AS (
                    setweight(to_tsvector('russian', coalesce(title, '')), 'A') ||
                    setweight(to_tsvector('russian', coalesce(body, '')), 'B')) STORED
);
CREATE INDEX kb_documents_tsv_idx ON kb.documents USING gin (tsv);
