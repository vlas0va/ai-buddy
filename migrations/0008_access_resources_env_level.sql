-- Accesses as at Avito (checkpoint 11.11): a request names a concrete resource (a Jira project, a Bitbucket repository,
-- a database), the environment and the level of rights; who approves depends on the resource. Synthetic data (R68).

ALTER TABLE access.resources DROP CONSTRAINT IF EXISTS resources_kind_check;
ALTER TABLE access.resources ADD CONSTRAINT resources_kind_check
    CHECK (kind IN ('tool','space','channel','project','repo','database'));
ALTER TABLE access.resources ADD COLUMN IF NOT EXISTS environments text[] NOT NULL DEFAULT '{}';  -- empty = no environments
ALTER TABLE access.resources ADD COLUMN IF NOT EXISTS levels text[] NOT NULL DEFAULT '{read}';
ALTER TABLE access.resources ADD COLUMN IF NOT EXISTS optional boolean NOT NULL DEFAULT false;     -- requested on demand
ALTER TABLE access.resources ADD COLUMN IF NOT EXISTS approver_label text NOT NULL DEFAULT 'руководитель команды';

ALTER TABLE access.requests ADD COLUMN IF NOT EXISTS environment text CHECK (environment IN ('dev','stage','prod'));
ALTER TABLE access.requests ADD COLUMN IF NOT EXISTS level text NOT NULL DEFAULT 'read'
    CHECK (level IN ('read','write','admin'));

UPDATE access.resources SET levels = '{read,write}' WHERE id IN ('confluence', 'jira', 'bitbucket');

INSERT INTO access.resources (id, name, kind, team_id, elevated, purpose, checklist_item_id, environments, levels, optional, approver_label) VALUES
    ('jira-dlv', 'Jira: проект DLV', 'project', 'delivery', false, 'Задачи и доска команды «Доставка»', 'CL-01', '{}', '{read,write,admin}', false, 'руководитель команды'),
    ('jira-srch', 'Jira: проект SRCH', 'project', 'search', false, 'Задачи и доска команды «Поиск»', 'CL-01', '{}', '{read,write,admin}', false, 'руководитель команды'),
    ('bb-delivery-api', 'Bitbucket: репозиторий delivery-api', 'repo', 'delivery', false, 'Код сервиса доставки', 'CL-01', '{}', '{read,write,admin}', false, 'руководитель команды (владелец репозитория)'),
    ('bb-search-indexer', 'Bitbucket: репозиторий search-indexer', 'repo', 'search', false, 'Код индексатора поиска', 'CL-01', '{}', '{read,write,admin}', false, 'руководитель команды (владелец репозитория)'),
    ('db-delivery-orders', 'БД delivery_orders (PostgreSQL)', 'database', 'delivery', false, 'Заказы доставки', NULL, '{dev,stage,prod}', '{read,write,admin}', true, 'руководитель команды, права выдаёт DevOps'),
    ('db-search-catalog', 'БД search_catalog (PostgreSQL)', 'database', 'search', false, 'Каталог поиска', NULL, '{dev,stage,prod}', '{read,write,admin}', true, 'руководитель команды, права выдаёт DevOps')
ON CONFLICT (id) DO NOTHING;
