-- Agent contract v1 (contracts/agent-stream.md): a lead can approve or reject in chat. Such a proposal has
-- kind approval_decision (draft.tool = approvals.decide); its result updates the intern's existing action, so
-- core.actions keeps its two kinds.
ALTER TABLE core.proposals DROP CONSTRAINT IF EXISTS proposals_kind_check;
ALTER TABLE core.proposals ADD CONSTRAINT proposals_kind_check
    CHECK (kind IN ('absence_request', 'access_request', 'approval_decision'));
