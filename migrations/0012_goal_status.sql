-- Goals of the adaptation (organizers 12.11): in the first week the intern and the lead agree on the goals and the
-- criteria of success; the lead approves them and follows their progress. buddy helps to write them down and track them.
-- proposed = the intern wrote it down after the meeting, waits for the mentor or the lead; active; achieved; missed.
ALTER TABLE core.goals ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'active'
    CHECK (status IN ('proposed', 'active', 'achieved', 'missed'));
