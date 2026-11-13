package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"aibuddy/services/internal/core/ratelimit"
)

func TestAllow_FixedWindow_R77(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	var _ ratelimit.Allower = ratelimit.New(rdb)
	l := ratelimit.New(rdb)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		ok, err := l.Allow(ctx, "chat:intern.anna", 3, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if want := i <= 3; ok != want {
			t.Fatalf("hit %d: allowed=%v want %v", i, ok, want)
		}
	}
	// другой ключ и другой пользователь независимы
	if ok, _ := l.Allow(ctx, "chat:intern.ivan", 3, time.Minute); !ok {
		t.Fatal("other user must be allowed")
	}
	// следующее окно начинается с нуля
	time.Sleep(1100 * time.Millisecond)
	if ok, _ := l.Allow(ctx, "chat:intern.anna", 3, time.Second); !ok {
		t.Fatal("new window must be allowed")
	}
}

func TestAllow_FailOpen_R77(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	defer rdb.Close()
	l := ratelimit.New(rdb)
	mr.Close()
	ok, err := l.Allow(context.Background(), "k", 1, time.Minute)
	if !ok || err == nil {
		t.Fatalf("redis down: ok=%v err=%v, want allowed with error", ok, err)
	}
}

func TestAllow_Disabled(t *testing.T) {
	ctx := context.Background()
	for name, l := range map[string]*ratelimit.Limiter{"nil": nil, "no redis": ratelimit.New(nil)} {
		if ok, err := l.Allow(ctx, "k", 1, time.Minute); !ok || err != nil {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	l := ratelimit.New(rdb)
	if ok, _ := l.Allow(ctx, "k", 0, time.Minute); !ok {
		t.Error("limit 0 must not limit")
	}
	if ok, _ := l.Allow(ctx, "k", 1, 0); !ok {
		t.Error("window 0 must not limit")
	}
}
