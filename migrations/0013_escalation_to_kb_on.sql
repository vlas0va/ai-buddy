-- The mentor's «add the answer to the knowledge base» works out of the box (feedback 12.13): the flag stays a feature
-- flag (it can be switched off per team), but the demo starts with it on.
UPDATE core.feature_flags SET enabled = true, updated_at = now() WHERE key = 'escalation_to_kb';
