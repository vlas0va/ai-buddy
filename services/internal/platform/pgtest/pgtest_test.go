package pgtest

import (
	"context"
	"testing"
)

func TestNewAppliesMigrationsAndSeed(t *testing.T) {
	pool := New(t)
	var users, items int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM core.users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM checklist.items").Scan(&items); err != nil {
		t.Fatal(err)
	}
	if users != 10 || items != 16 { // 9 seeded + the HR admin (0007)
		t.Fatalf("seed not applied: users=%d items=%d", users, items)
	}
	if URL(t, pool) == "" {
		t.Fatal("empty url")
	}
}

func TestWithDatabase(t *testing.T) {
	got, err := withDatabase("postgres://u:p@h:1/postgres?sslmode=disable", "t_x")
	if err != nil || got != "postgres://u:p@h:1/t_x?sslmode=disable" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := withDatabase("://bad", "x"); err == nil {
		t.Fatal("expected error")
	}
}
