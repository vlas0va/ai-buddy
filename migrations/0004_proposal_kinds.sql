-- Контракт агента v1 (contracts/agent-stream.md): руководитель может согласовать или отклонить в чате. У такого
-- черновика вид approval_decision (draft.tool = approvals.decide); его результат обновляет существующее действие
-- стажёра, поэтому в core.actions остаются два вида.
ALTER TABLE core.proposals DROP CONSTRAINT IF EXISTS proposals_kind_check;
ALTER TABLE core.proposals ADD CONSTRAINT proposals_kind_check
    CHECK (kind IN ('absence_request', 'access_request', 'approval_decision'));
