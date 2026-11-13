//go:build tools

// Пакет tools закрепляет сторонние зависимости, чтобы `go mod tidy` и `go mod vendor` сохраняли их, пока ни один пакет
// их ещё не импортирует. В бинарники никогда не собирается.
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
