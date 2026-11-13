-- Отчёт за стажировку (идея 12.11): чтобы различать «раньше срока», «в срок» и «с опозданием», задача хранит момент
-- выполнения и признак, была ли она когда-либо просрочена. Триггер держит оба поля в согласии со статусом на любом пути
-- кода.
ALTER TABLE core.plan_tasks ADD COLUMN IF NOT EXISTS completed_at timestamptz;
ALTER TABLE core.plan_tasks ADD COLUMN IF NOT EXISTS was_overdue boolean NOT NULL DEFAULT false;

UPDATE core.plan_tasks SET completed_at = updated_at WHERE status = 'done' AND completed_at IS NULL;
UPDATE core.plan_tasks SET was_overdue = true WHERE status = 'overdue';

CREATE OR REPLACE FUNCTION core.plan_tasks_track_completion() RETURNS trigger AS $$
BEGIN
    IF NEW.status = 'done' AND (TG_OP = 'INSERT' OR OLD.status <> 'done') THEN
        NEW.completed_at := coalesce(NEW.completed_at, now());
    ELSIF NEW.status <> 'done' THEN
        NEW.completed_at := NULL;
    END IF;
    IF NEW.status = 'overdue' THEN
        NEW.was_overdue := true;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS plan_tasks_track_completion ON core.plan_tasks;
CREATE TRIGGER plan_tasks_track_completion BEFORE INSERT OR UPDATE OF status ON core.plan_tasks
    FOR EACH ROW EXECUTE FUNCTION core.plan_tasks_track_completion();

-- Демо: Анна закрыла CL-03 через два дня после начала, раньше срока (синтетические данные, R68).
UPDATE core.plan_tasks t SET completed_at = u.cohort_start + 2
FROM core.plans p JOIN core.users u ON u.id = p.user_id
WHERE t.plan_id = p.id AND p.user_id = 'intern.anna' AND p.plan_role = 'intern'
  AND t.checklist_item_id = 'CL-03' AND t.parent_task_id IS NULL AND t.status = 'done';
