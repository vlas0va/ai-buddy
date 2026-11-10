-- Business trips, employee consents and the HR steps of a day off (case «Пространство решений»: absences, trips,
-- consents; organizers' Q&A 10.11: a day off involves the lead, the HR admin and the HR BP). Synthetic data (R68).

-- directory: the HR administrator who registers approved absences, and the office of every employee
ALTER TABLE directory.employees DROP CONSTRAINT IF EXISTS employees_role_check;
ALTER TABLE directory.employees ADD CONSTRAINT employees_role_check
    CHECK (role IN ('intern','mentor','lead','hrbp','alumni','curator','hr_admin'));
ALTER TABLE directory.employees ADD COLUMN IF NOT EXISTS location text;
INSERT INTO directory.employees (id, display_name, role, team_id, channel)
VALUES ('hradmin.olga', 'Ольга Смирнова', 'hr_admin', 'people', '#hr-questions')
ON CONFLICT (id) DO NOTHING;
UPDATE directory.employees SET location = CASE
    WHEN team_id = 'search' THEN 'Санкт-Петербург'
    WHEN role = 'alumni' THEN 'Удалённо'
    ELSE 'Москва'
END
WHERE location IS NULL;

-- absence: lead approves → HR admin registers in the HR system → HR BP is notified
ALTER TABLE absence.requests ADD COLUMN IF NOT EXISTS hr_admin_id text REFERENCES directory.employees(id);
ALTER TABLE absence.requests ADD COLUMN IF NOT EXISTS registered_at timestamptz;
ALTER TABLE absence.requests ADD COLUMN IF NOT EXISTS hrbp_id text REFERENCES directory.employees(id);
ALTER TABLE absence.requests ADD COLUMN IF NOT EXISTS hrbp_notified_at timestamptz;

-- trips: business trip system imitation
CREATE SCHEMA IF NOT EXISTS trips;
CREATE SEQUENCE IF NOT EXISTS trips.request_seq START 200;
CREATE TABLE IF NOT EXISTS trips.requests (
    id              text PRIMARY KEY DEFAULT 'TRP-' || lpad(nextval('trips.request_seq')::text, 6, '0'),
    employee_id     text NOT NULL REFERENCES directory.employees(id),
    city            text NOT NULL,
    start_date      date NOT NULL,
    end_date        date NOT NULL CHECK (end_date >= start_date),
    purpose         text NOT NULL,
    status          text NOT NULL DEFAULT 'pending_approval'
                    CHECK (status IN ('pending_approval','approved','rejected','cancelled')),
    approver_id     text NOT NULL REFERENCES directory.employees(id),
    idempotency_key uuid NOT NULL UNIQUE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    decided_at      timestamptz
);
CREATE INDEX IF NOT EXISTS trips_requests_employee_idx ON trips.requests (employee_id, start_date);

-- consents: what an employee agreed to; some are signed only in the HR portal at hire
CREATE SCHEMA IF NOT EXISTS consents;
CREATE TABLE IF NOT EXISTS consents.types (
    id          text PRIMARY KEY,
    title       text NOT NULL,
    description text NOT NULL,
    signable    boolean NOT NULL,   -- false: signed in the HR portal, buddy only shows the status
    required    boolean NOT NULL,
    position    int NOT NULL
);
CREATE SEQUENCE IF NOT EXISTS consents.signature_seq START 300;
CREATE TABLE IF NOT EXISTS consents.signatures (
    id              text PRIMARY KEY DEFAULT 'CNS-' || lpad(nextval('consents.signature_seq')::text, 6, '0'),
    employee_id     text NOT NULL REFERENCES directory.employees(id),
    consent_id      text NOT NULL REFERENCES consents.types(id),
    signed_at       timestamptz NOT NULL DEFAULT now(),
    via             text NOT NULL DEFAULT 'buddy' CHECK (via IN ('hr_portal','buddy')),
    idempotency_key uuid UNIQUE,
    UNIQUE (employee_id, consent_id)
);
INSERT INTO consents.types (id, title, description, signable, required, position) VALUES
    ('pdn', 'Обработка персональных данных', 'Подписывается при оформлении в кадровом портале', false, true, 1),
    ('nda', 'Соглашение о конфиденциальности', 'Подписывается при оформлении в кадровом портале', false, true, 2),
    ('photo', 'Фото и видео во внутренних материалах', 'Можно использовать ваши фото и видео в рассылках и на внутреннем портале', true, false, 3),
    ('surveys', 'Участие в опросах стажёров', 'Короткие опросы на 2-й неделе и на экваторе, ответы обезличены', true, false, 4)
ON CONFLICT (id) DO NOTHING;
INSERT INTO consents.signatures (employee_id, consent_id, via, signed_at)
SELECT e.id, t.id, 'hr_portal', coalesce(e.cohort_start::timestamptz, now())
FROM directory.employees e CROSS JOIN consents.types t
WHERE t.signable = false AND e.role IN ('intern','mentor','lead','hrbp','alumni','curator','hr_admin')
ON CONFLICT DO NOTHING;

-- approvals and core: new kinds
ALTER TABLE approvals.items DROP CONSTRAINT IF EXISTS items_kind_check;
ALTER TABLE approvals.items ADD CONSTRAINT items_kind_check
    CHECK (kind IN ('absence_request','access_request','trip_request'));
ALTER TABLE core.actions DROP CONSTRAINT IF EXISTS actions_kind_check;
ALTER TABLE core.actions ADD CONSTRAINT actions_kind_check
    CHECK (kind IN ('absence_request','access_request','trip_request','consent'));
ALTER TABLE core.actions DROP CONSTRAINT IF EXISTS actions_status_check;
ALTER TABLE core.actions ADD CONSTRAINT actions_status_check
    CHECK (status IN ('pending_approval','approved','rejected','granted','created','failed','signed'));
ALTER TABLE core.proposals DROP CONSTRAINT IF EXISTS proposals_kind_check;
ALTER TABLE core.proposals ADD CONSTRAINT proposals_kind_check
    CHECK (kind IN ('absence_request','access_request','approval_decision','trip_request','consent'));

-- core: attachments of a user message (metadata; text files also keep their text, images and documents never do)
ALTER TABLE core.chat_messages ADD COLUMN IF NOT EXISTS attachments jsonb NOT NULL DEFAULT '[]';
