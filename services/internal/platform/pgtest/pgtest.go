// Пакет pgtest даёт тестам свежую базу PostgreSQL, собранную из настоящих миграций (схема + демо-данные).
//
// Задайте TEST_DATABASE_URL — подключение суперпользователя (его печатает scripts/test-db.sh). Каждый вызов New создаёт
// базу t_<random>, применяет migrations.FS в лексикографическом порядке и удаляет базу после теста.
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

// EnvVar — переменная со строкой подключения администратора.
const EnvVar = "TEST_DATABASE_URL"

// New возвращает пул, подключённый к свежей базе с миграциями, или пропускает тест, если TEST_DATABASE_URL не задан.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	admin := os.Getenv(EnvVar)
	if admin == "" {
		t.Skipf("%s is not set; run `bash scripts/test-db.sh up` or `make test`", EnvVar)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	name := newName()
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

// URL возвращает строку подключения к базе за pool (полезно для сервисов, открывающих свой пул).
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
		// Простой протокол: файл миграции — много запросов в одной строке.
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

// newName возвращает имя новой тестовой базы; это переменная, чтобы тесты могли следить ровно за одной базой.
var newName = func() string { return "t_" + randomHex(8) }
