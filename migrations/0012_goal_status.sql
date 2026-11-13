-- Цели адаптации (организаторы 12.11): в первую неделю стажёр и руководитель договариваются о целях и критериях успеха;
-- руководитель утверждает их и следит за выполнением. buddy помогает их записать и отслеживать. proposed — стажёр
-- записал цель после встречи, она ждёт наставника или руководителя; active; achieved; missed.
ALTER TABLE core.goals ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'active'
    CHECK (status IN ('proposed', 'active', 'achieved', 'missed'));
