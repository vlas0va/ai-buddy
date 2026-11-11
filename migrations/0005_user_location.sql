-- Office location: personal data the organizers allow buddy to use (Q&A 10.11). Sent to the agent as
-- user_context.location; the agent picks the matching KB section («Москва», «Санкт-Петербург», «Удалённо»).
ALTER TABLE core.users ADD COLUMN IF NOT EXISTS location text;

UPDATE core.users SET location = CASE
    WHEN team_id = 'search' THEN 'Санкт-Петербург'
    WHEN role = 'alumni' THEN 'Удалённо'
    ELSE 'Москва'
END
WHERE location IS NULL;
