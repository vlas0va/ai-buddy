// Package pgtest gives tests a fresh PostgreSQL database built from the real migrations (schema + demo seed).
//
// Set TEST_DATABASE_URL to a superuser connection (scripts/test-db.sh prints one). Each New call creates
// database t_<random>, applies migrations.FS in lexical order and drops the database when the test ends.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/migrations"
)

// EnvVar is the variable holding the admin connection string.
const EnvVar = "TEST_DATABASE_URL"

// New returns a pool connected to a fresh migrated database, or skips the test when TEST_DATABASE_URL is unset.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	admin := os.Getenv(EnvVar)
	if admin == "" {
		t.Skipf("%s is not set; run `bash scripts/test-db.sh up` or `make test`", EnvVar)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	name := "t_" + randomHex(8)
	adminConn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("pgtest: connect admin: %v", err)
	}
	defer adminConn.Close(context.Background())
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("pgtest: create database: %v", err)
	}
	dbURL, err := withDatabase(admin, name)
	if err != nil {
		t.Fatalf("pgtest: %v", err)
	}
	if err := applyMigrations(ctx, dbURL); err != nil {
		dropDatabase(admin, name)
		t.Fatalf("pgtest: %v", err)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		dropDatabase(admin, name)
		t.Fatalf("pgtest: open pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropDatabase(admin, name)
	})
	return pool
}

// URL returns the connection string of the database behind pool (useful for services that open their own pool).
func URL(t testing.TB, pool *pgxpool.Pool) string {
	t.Helper()
	return pool.Config().ConnString()
}

func applyMigrations(ctx context.Context, dbURL string) error {
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect test database: %w", err)
	}
	defer conn.Close(context.Background())
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)
	for _, n := range names {
		sql, err := migrations.FS.ReadFile(n)
		if err != nil {
			return fmt.Errorf("read %s: %w", n, err)
		}
		// Simple protocol: a migration file is many statements in one string.
		if _, err := conn.Exec(ctx, string(sql), pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("apply %s: %w", n, err)
		}
	}
	return nil
}

func withDatabase(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", EnvVar, err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

func dropDatabase(admin, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return
	}
	defer conn.Close(context.Background())
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
