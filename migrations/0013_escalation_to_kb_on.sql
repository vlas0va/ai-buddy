-- «Добавить ответ в базу знаний» у наставника работает сразу (отзыв 12.13): флаг остаётся feature flag (его можно
-- выключить для команды), но демо стартует с включённым.
UPDATE core.feature_flags SET enabled = true, updated_at = now() WHERE key = 'escalation_to_kb';
