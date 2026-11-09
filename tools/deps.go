//go:build tools

// Package tools pins third-party dependencies so `go mod tidy` and `go mod vendor`
// keep them before every package imports them. Never built into binaries.
package tools

import (
	_ "github.com/alicebob/miniredis/v2"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/modelcontextprotocol/go-sdk/mcp"
	_ "github.com/redis/go-redis/v9"
	_ "github.com/twmb/franz-go/pkg/kgo"
	_ "gopkg.in/yaml.v3"
)
