package absence

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/mcpx"
	"aibuddy/services/internal/platform/pgtest"
)

var (
	jwtSecret  = []byte("test-jwt-secret")
	confSecret = []byte("test-confirm-secret")
)

// monday is Monday 2026-11-09 12:00 Moscow time: tomorrow is Tuesday 2026-11-10, 14-15 Nov is a weekend.
var monday = time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)

// Demo seed people (migrations/0003_seed_demo.sql).
var (
	anna  = caller("intern.anna", "intern", "delivery") // lead: lead.maria
	ivan  = caller("intern.ivan", "intern", "search")   // lead: lead.dmitry
	oleg  = caller("mentor.oleg", "mentor", "delivery") // lead: lead.maria
	maria = caller("lead.maria", "lead", "delivery")    // no lead
)

func caller(sub, role, team string) mcpx.Caller {
	return mcpx.Caller{Claims: authn.Claims{Sub: sub, Role: role, TeamID: team}}
}

// clock is an adjustable test clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

type env struct {
	t     *testing.T
	pool  *pgxpool.Pool
	srv   *mcpx.Server
	clock *clock
}

// newEnv returns an absence server over a fresh migrated database with the clock at monday.
func newEnv(t *testing.T) *env {
	t.Helper()
	pool := pgtest.New(t)
	e := &env{t: t, pool: pool, clock: &clock{now: monday}}
	e.srv = newServer(t, pool, e.clock.Now)
	return e
}

func newServer(t *testing.T, pool *pgxpool.Pool, now func() time.Time) *mcpx.Server {
	t.Helper()
	srv, err := mcpx.NewServer("absence", mcpx.Config{
		JWTSecret: jwtSecret, ConfirmationSecret: confSecret, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := register(srv, pool, now); err != nil {
		t.Fatal(err)
	}
	if err := srv.Validate(); err != nil {
		t.Fatal(err)
	}
	return srv
}

func (e *env) call(c mcpx.Caller, tool string, args any) (json.RawMessage, *apierr.Error) {
	e.t.Helper()
	return e.srv.Invoke(context.Background(), c, tool, args)
}

// mustCall fails the test on a tool error and decodes the result into T.
func mustCall[T any](e *env, c mcpx.Caller, tool string, args any) T {
	e.t.Helper()
	raw, terr := e.call(c, tool, args)
	if terr != nil {
		e.t.Fatalf("%s as %s: %v", tool, c.Sub, terr)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

// wantCode fails unless the call returned a tool error with code.
func (e *env) wantCode(c mcpx.Caller, tool string, args any, code apierr.Code) *apierr.Error {
	e.t.Helper()
	raw, terr := e.call(c, tool, args)
	if terr == nil || terr.Code != code {
		e.t.Fatalf("%s as %s: want %s, got %v / %s", tool, c.Sub, code, terr, raw)
	}
	return terr
}

// confirmed returns args with idempotency_key and a valid confirmation token for c.
func confirmed(t *testing.T, c mcpx.Caller, key string, args map[string]any) map[string]any {
	t.Helper()
	out, err := mcpx.WithConfirmation(confSecret, c.Sub, key, args, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func dayOff(start, end string) map[string]any {
	return map[string]any{"type": "day_off", "start_date": start, "end_date": end}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("exec %q: %v", sql, err)
	}
}

type requestOut struct {
	ID                  string `json:"id"`
	Type                string `json:"type"`
	StartDate           string `json:"start_date"`
	EndDate             string `json:"end_date"`
	Comment             string `json:"comment"`
	Status              string `json:"status"`
	ApproverID          string `json:"approver_id"`
	ApproverDisplayName string `json:"approver_display_name"`
	CreatedAt           string `json:"created_at"`
	Replayed            *bool  `json:"replayed"`
}

type validateResult struct {
	Valid               bool     `json:"valid"`
	Problems            []string `json:"problems"`
	ApproverID          string   `json:"approver_id"`
	ApproverDisplayName string   `json:"approver_display_name"`
}
