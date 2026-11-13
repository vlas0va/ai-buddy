-- Схемы мок-сервисов Авито за MCP (владелец B, R39, R52, R56). Одна схема на сервер. Только синтетические данные (R68).
-- Они имитируют существующие системы; это не собственные HR-системы (R70).

CREATE SCHEMA IF NOT EXISTS directory;
CREATE SCHEMA IF NOT EXISTS checklist;
CREATE SCHEMA IF NOT EXISTS absence;
CREATE SCHEMA IF NOT EXISTS approvals;
CREATE SCHEMA IF NOT EXISTS access;
CREATE SCHEMA IF NOT EXISTS kb;

-- directory: имитация данных сотрудников
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
    channel      text                            -- название рабочего чата, без личных контактов
);

-- checklist: чек-лист стажёра Авито (data/checklist/checklist.yaml)
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

-- absence: имитация системы отсутствий (S3)
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

-- access: имитация доступов и каналов (S4)
CREATE TABLE access.resources (
    id                text PRIMARY KEY,
    name              text NOT NULL,
    kind              text NOT NULL CHECK (kind IN ('tool','space','channel')),
    team_id           text REFERENCES directory.teams(id),  -- NULL — обязательно для всех команд
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

-- approvals: имитация системы согласований («Согласия»), общая для S3 и S4
CREATE TABLE approvals.items (
    id              text PRIMARY KEY DEFAULT 'APR-' || substr(md5(gen_random_uuid()::text), 1, 8),
    kind            text NOT NULL CHECK (kind IN ('absence_request','access_request')),
    ref_id          text NOT NULL,              -- ABS-... or ACC-...
    requester_id    text NOT NULL REFERENCES directory.employees(id),
    approver_id     text NOT NULL REFERENCES directory.employees(id),
    summary         text NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    comment         text,
    idempotency_key uuid UNIQUE,                -- вызова decide
    created_at      timestamptz NOT NULL DEFAULT now(),
    decided_at      timestamptz,
    UNIQUE (kind, ref_id)
);
CREATE INDEX approvals_approver_idx ON approvals.items (approver_id, status);

-- kb: база знаний с ACL, применяемым до того, как что-либо попадёт в LLM (R71), и русским полнотекстовым поиском
CREATE TABLE kb.documents (
    doc_id      text PRIMARY KEY,
    title       text NOT NULL,
    body        text NOT NULL,
    source_type text NOT NULL DEFAULT 'test' CHECK (source_type = 'test'),  -- R69
    acl_roles   text[] NOT NULL,                -- например {intern,mentor,lead}
    acl_teams   text[] NOT NULL,                -- id команд или {*}
    kind        text NOT NULL DEFAULT 'doc' CHECK (kind IN ('doc','faq','note')),  -- faq — ответ наставника, note — личные заметки
    owner_id    text REFERENCES directory.employees(id),  -- задаётся только для личных заметок (CL-04 шаг 3)
    created_by  text REFERENCES directory.employees(id),
    idempotency_key uuid UNIQUE,                -- kb.add_faq
    checklist_item_id text,
    url         text,
    updated     date NOT NULL DEFAULT current_date,
    tsv         tsvector GENERATED ALWAYS AS (
                    setweight(to_tsvector('russian', coalesce(title, '')), 'A') ||
                    setweight(to_tsvector('russian', coalesce(body, '')), 'B')) STORED
);
CREATE INDEX kb_documents_tsv_idx ON kb.documents USING gin (tsv);
