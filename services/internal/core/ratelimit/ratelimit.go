// Пакет ratelimit — ограничитель частоты на Redis с фиксированным окном (для сообщений чата от пользователя, R76-R79).
// При сбое пропускает: если Redis недоступен, запрос разрешается, а ошибка возвращается для логирования.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Allower — то, от чего зависят вызывающие, чтобы тесты могли подменить ограничитель.
type Allower interface {
	// Allow засчитывает одно обращение по key и сообщает, укладывается ли оно в limit обращений за window. При ошибке
	// возвращает true (пропускает).
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

// Limiter реализует Allower на Redis.
type Limiter struct {
	rdb redis.Cmdable
	now func() time.Time
}

// New возвращает Limiter. Nil rdb означает «без ограничения».
func New(rdb redis.Cmdable) *Limiter {
	return &Limiter{rdb: rdb, now: time.Now}
}

// Allow реализует Allower. Окно выровнено по эпохе Unix (фиксированное окно); неположительные limit или window
// отключают ограничение для вызова.
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
