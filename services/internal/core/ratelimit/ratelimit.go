// Package ratelimit is a Redis fixed-window rate limiter (used for chat messages per user, R76-R79).
// It fails open: when Redis is unreachable the request is allowed and the error is returned for logging.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Allower is what callers depend on, so tests can fake the limiter.
type Allower interface {
	// Allow counts one hit for key and reports whether it is within limit hits per window.
	// On error the returned bool is true (fail-open).
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

// Limiter implements Allower on Redis.
type Limiter struct {
	rdb redis.Cmdable
	now func() time.Time
}

// New returns a Limiter. A nil rdb means "no limiting".
func New(rdb redis.Cmdable) *Limiter {
	return &Limiter{rdb: rdb, now: time.Now}
}

// Allow implements Allower. The window is aligned to the Unix epoch (fixed window); a non-positive limit or
// window disables limiting for the call.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	if l == nil || l.rdb == nil || limit <= 0 || window <= 0 {
		return true, nil
	}
	bucket := l.now().UnixNano() / int64(window)
	rkey := fmt.Sprintf("rl:%s:%d", key, bucket)
	pipe := l.rdb.TxPipeline()
	incr := pipe.Incr(ctx, rkey)
	pipe.Expire(ctx, rkey, window+time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return true, fmt.Errorf("rate limit %q: %w", key, err)
	}
	return incr.Val() <= int64(limit), nil
}
