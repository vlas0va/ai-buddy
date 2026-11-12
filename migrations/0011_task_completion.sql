-- Internship report (idea 12.11): to tell «раньше срока», «в срок» and «с опозданием» apart a task keeps the moment it
-- was done and whether it was ever overdue. A trigger keeps both in step with status on every code path.
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

-- Demo: Анна closed CL-03 two days after the start, ahead of its due date (synthetic data, R68).
UPDATE core.plan_tasks t SET completed_at = u.cohort_start + 2
FROM core.plans p JOIN core.users u ON u.id = p.user_id
WHERE t.plan_id = p.id AND p.user_id = 'intern.anna' AND p.plan_role = 'intern'
  AND t.checklist_item_id = 'CL-03' AND t.parent_task_id IS NULL AND t.status = 'done';
