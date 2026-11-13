// Пакет pg открывает пулы соединений PostgreSQL (pgx/v5) для ядра и mcp-mocks.
package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Options настраивают Open. Нулевые значения — значения по умолчанию.
type Options struct {
	MaxConns     int32         // по умолчанию 10
	StartupWait  time.Duration // сколько повторять, пока Postgres ещё стартует; по умолчанию 30 с
	RetryBackoff time.Duration // пауза между попытками; по умолчанию 1 с
}

// Open подключается к url и пингует его, повторяя, пока не истечёт opts.StartupWait (при старте сервиса Postgres может
// ещё выполнять init-скрипты).
func Open(ctx context.Context, url string, opts Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	cfg.MaxConns = opts.MaxConns
	if cfg.MaxConns == 0 {
		cfg.MaxConns = 10
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	wait, backoff := opts.StartupWait, opts.RetryBackoff
	if wait == 0 {
		wait = 30 * time.Second
	}
	if backoff == 0 {
		backoff = time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connect postgres: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
}
