// Package pg opens PostgreSQL connection pools (pgx/v5) for core and mcp-mocks.
package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Options tune Open. Zero values mean defaults.
type Options struct {
	MaxConns     int32         // default 10
	StartupWait  time.Duration // how long to retry while Postgres is still starting; default 30s
	RetryBackoff time.Duration // pause between attempts; default 1s
}

// Open connects to url and pings it, retrying until opts.StartupWait elapses (Postgres may still be
// running init scripts when the service starts).
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
