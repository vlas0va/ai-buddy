package pgtest

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeTB records Skipf/Fatalf of New and stops its goroutine like the real testing.T does.
type fakeTB struct {
	testing.TB
	kind, msg string
	cleanups  []func()
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Skipf(format string, args ...any) {
	f.kind, f.msg = "skip", fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.kind, f.msg = "fatal", fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func (f *fakeTB) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }

func runNew(t *testing.T) *fakeTB {
	t.Helper()
	f := &fakeTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		New(f)
	}()
	<-done
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
	return f
}

func TestNew_SkipsWithoutURL(t *testing.T) {
	t.Setenv(EnvVar, "")
	if f := runNew(t); f.kind != "skip" || !strings.Contains(f.msg, EnvVar) {
		t.Fatalf("got %s %q", f.kind, f.msg)
	}
}

func TestNew_FailsOnUnreachableAdmin(t *testing.T) {
	t.Setenv(EnvVar, "postgres://x:y@127.0.0.1:1/postgres?sslmode=disable&connect_timeout=1")
	if f := runNew(t); f.kind != "fatal" || !strings.Contains(f.msg, "connect admin") {
		t.Fatalf("got %s %q", f.kind, f.msg)
	}
}

func TestNew_FailsWithoutCreateDBRight(t *testing.T) {
	admin := os.Getenv(EnvVar)
	if admin == "" {
		t.Skipf("%s is not set", EnvVar)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	role := "pgtest_nocreate_" + randomHex(4)
	if _, err := conn.Exec(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD 'pw' NOCREATEDB"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(ctx, "DROP ROLE IF EXISTS "+role) }()
	u := strings.Replace(admin, "buddy:buddy@", role+":pw@", 1)
	if u == admin {
		t.Skip("admin URL does not use buddy:buddy credentials")
	}
	t.Setenv(EnvVar, u)
	if f := runNew(t); f.kind != "fatal" || !strings.Contains(f.msg, "create database") {
		t.Fatalf("got %s %q", f.kind, f.msg)
	}
}

// A keyword/value DSN connects as admin but cannot be rewritten for the test database: New must fail and
// still drop the database it created.
func TestNew_FailsOnKeywordDSNAndDropsDatabase(t *testing.T) {
	admin := os.Getenv(EnvVar)
	if admin == "" {
		t.Skipf("%s is not set", EnvVar)
	}
	cfg, err := pgx.ParseConfig(admin)
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database)
	// Follow this test's own database by name: other packages create and drop test databases concurrently.
	name := "t_keyword_dsn_" + randomHex(4)
	orig := newName
	newName = func() string { return name }
	defer func() { newName = orig }()
	t.Setenv(EnvVar, dsn)
	f := runNew(t)
	if f.kind != "fatal" {
		t.Fatalf("got %s %q", f.kind, f.msg)
	}
	conn, err := pgx.Connect(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var exists bool
	if err := conn.QueryRow(context.Background(), "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("test database %s leaked", name)
	}
}

func TestDropDatabase_UnreachableAdminIsIgnored(t *testing.T) {
	dropDatabase("postgres://x:y@127.0.0.1:1/postgres?sslmode=disable&connect_timeout=1", "t_none")
}
