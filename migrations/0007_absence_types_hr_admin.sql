-- Заявка на отсутствие как в Авито (чекпоинт 11.11): одна заявка с видом (отпуск, отгул, отсутствие, декрет),
-- необязательные документы, согласование руководителем, а затем HR-администратором; пока HR-администратор не
-- согласовал, даты можно перенести. Синтетические данные (R68).

-- absence: виды, документы, этап между руководителем и HR-администратором
ALTER TABLE absence.requests DROP CONSTRAINT IF EXISTS requests_type_check;
ALTER TABLE absence.requests ADD CONSTRAINT requests_type_check
    CHECK (type IN ('day_off','vacation','absence','parental_leave'));
ALTER TABLE absence.requests DROP CONSTRAINT IF EXISTS requests_status_check;
ALTER TABLE absence.requests ADD CONSTRAINT requests_status_check
    CHECK (status IN ('pending_approval','pending_hr','approved','rejected','cancelled'));
ALTER TABLE absence.requests ADD COLUMN IF NOT EXISTS documents jsonb NOT NULL DEFAULT '[]';
ALTER TABLE absence.requests ADD COLUMN IF NOT EXISTS lead_decided_at timestamptz;

-- approvals: на каком этапе пункт (руководитель, затем HR-администратор); перенос заявки отменяет её открытый пункт
ALTER TABLE approvals.items ADD COLUMN IF NOT EXISTS stage text NOT NULL DEFAULT 'lead'
    CHECK (stage IN ('lead','hr_admin'));
ALTER TABLE approvals.items DROP CONSTRAINT IF EXISTS items_status_check;
ALTER TABLE approvals.items ADD CONSTRAINT items_status_check
    CHECK (status IN ('pending','approved','rejected','cancelled'));

-- core: HR-администратор входит в систему и решает второй этап; действие ждёт HR-администратора
ALTER TABLE core.users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE core.users ADD CONSTRAINT users_role_check
    CHECK (role IN ('intern','mentor','lead','hrbp','alumni','curator','hr_admin'));
INSERT INTO core.users (id, display_name, role, team_id, cohort_start, password_hash, can_login)
VALUES ('hradmin.olga', 'Ольга Смирнова', 'hr_admin', 'people', NULL, crypt('demo', gen_salt('bf', 8)), true)
ON CONFLICT (id) DO NOTHING;
ALTER TABLE core.actions DROP CONSTRAINT IF EXISTS actions_status_check;
ALTER TABLE core.actions ADD CONSTRAINT actions_status_check
    CHECK (status IN ('pending_approval','pending_hr','approved','rejected','granted','created','failed','signed'));
ALTER TABLE core.proposals DROP CONSTRAINT IF EXISTS proposals_kind_check;
ALTER TABLE core.proposals ADD CONSTRAINT proposals_kind_check
    CHECK (kind IN ('absence_request','access_request','approval_decision','trip_request','consent','absence_reschedule'));
ALTER TABLE absence.requests ADD COLUMN IF NOT EXISTS reschedule_key uuid UNIQUE;
-- один открытый пункт на заявку (этап HR-администратора и перенос заявки добавляют новые пункты после закрытия
-- прежнего)
ALTER TABLE approvals.items DROP CONSTRAINT IF EXISTS items_kind_ref_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS approvals_items_open_idx ON approvals.items (kind, ref_id) WHERE status = 'pending';
