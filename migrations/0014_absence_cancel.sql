-- Отзыв 12.13: заявку на отсутствие можно отозвать, пока HR-администратор её не согласовал (а более длинное отсутствие,
-- накрывающее несогласованный отгул, заменяет его за один шаг).
ALTER TABLE absence.requests ADD COLUMN IF NOT EXISTS cancel_key uuid UNIQUE;
ALTER TABLE core.proposals DROP CONSTRAINT IF EXISTS proposals_kind_check;
ALTER TABLE core.proposals ADD CONSTRAINT proposals_kind_check
    CHECK (kind IN ('absence_request','access_request','approval_decision','trip_request','consent','absence_reschedule',
                    'absence_cancel'));
ALTER TABLE core.actions DROP CONSTRAINT IF EXISTS actions_status_check;
ALTER TABLE core.actions ADD CONSTRAINT actions_status_check
    CHECK (status IN ('pending_approval','pending_hr','approved','rejected','granted','created','failed','signed',
                      'cancelled'));
