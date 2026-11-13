// Пакет migrations встраивает SQL-миграции, чтобы тесты могли собрать чистую базу (platform/pgtest). Docker при первом
// запуске выполняет те же файлы *.sql из /docker-entrypoint-initdb.d.
package migrations

import "embed"

// FS содержит 0001_core.sql, 0002_mocks.sql, 0003_seed_demo.sql, ... — применяются в лексикографическом порядке.
//
//go:embed *.sql
var FS embed.FS
