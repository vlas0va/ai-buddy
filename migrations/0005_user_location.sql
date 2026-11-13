-- Офис: персональные данные, которые организаторы разрешают использовать buddy (ответы 10.11). Передаётся агенту как
-- user_context.location; агент выбирает подходящий раздел базы знаний («Москва», «Санкт-Петербург», «Удалённо»).
ALTER TABLE core.users ADD COLUMN IF NOT EXISTS location text;

UPDATE core.users SET location = CASE
    WHEN team_id = 'search' THEN 'Санкт-Петербург'
    WHEN role = 'alumni' THEN 'Удалённо'
    ELSE 'Москва'
END
WHERE location IS NULL;
