// Package migrations embeds the SQL migrations so tests can build a fresh database (platform/pgtest).
// Docker runs the same *.sql files from /docker-entrypoint-initdb.d on first start.
package migrations

import "embed"

// FS holds 0001_core.sql, 0002_mocks.sql, 0003_seed_demo.sql, ... applied in lexical order.
//
//go:embed *.sql
var FS embed.FS
