package pg

import (
	"context"
	"testing"
	"time"

	"aibuddy/services/internal/platform/pgtest"
)

func TestOpenConnects(t *testing.T) {
	ref := pgtest.New(t)
	pool, err := Open(context.Background(), pgtest.URL(t, ref), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if pool.Config().MaxConns != 10 {
		t.Fatalf("max conns %d", pool.Config().MaxConns)
	}
}

func TestOpenFailsAfterStartupWait(t *testing.T) {
	_, err := Open(context.Background(), "postgres://x:y@127.0.0.1:1/db?sslmode=disable&connect_timeout=1",
		Options{StartupWait: 50 * time.Millisecond, RetryBackoff: 10 * time.Millisecond, MaxConns: 2})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenBadURL(t *testing.T) {
	if _, err := Open(context.Background(), "::nope", Options{}); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestOpenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, "postgres://x:y@127.0.0.1:1/db?sslmode=disable&connect_timeout=1", Options{StartupWait: time.Minute}); err == nil {
		t.Fatal("expected error")
	}
}
