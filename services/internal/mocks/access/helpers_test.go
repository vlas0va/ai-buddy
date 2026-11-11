package access

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
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

// Demo seed people (migrations/0003_seed_demo.sql).
var (
	anna  = caller("intern.anna", "intern", "delivery") // lead: lead.maria
	ivan  = caller("intern.ivan", "intern", "search")   // lead: lead.dmitry
	oleg  = caller("mentor.oleg", "mentor", "delivery") // lead: lead.maria
	maria = caller("lead.maria", "lead", "delivery")
)

func caller(sub, role, team string) mcpx.Caller {
	return mcpx.Caller{Claims: authn.Claims{Sub: sub, Role: role, TeamID: team}}
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	srv  *mcpx.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := pgtest.New(t)
	return &env{t: t, pool: pool, srv: newServer(t, pool)}
}

func newServer(t *testing.T, pool *pgxpool.Pool) *mcpx.Server {
	t.Helper()
	srv, err := mcpx.NewServer("access", mcpx.Config{
		JWTSecret: jwtSecret, ConfirmationSecret: confSecret, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(srv, pool); err != nil {
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

func (e *env) wantCode(c mcpx.Caller, tool string, args any, code apierr.Code) *apierr.Error {
	e.t.Helper()
	raw, terr := e.call(c, tool, args)
	if terr == nil || terr.Code != code {
		e.t.Fatalf("%s as %s: want %s, got %v / %s", tool, c.Sub, code, terr, raw)
	}
	return terr
}

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

func resource(id string) map[string]any { return map[string]any{"resource_id": id} }

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

// addRequest inserts a request of employee for resource with status, created minutes ago.
func (e *env) addRequest(employee, resource, status string, minutesAgo int) {
	e.t.Helper()
	e.exec(`INSERT INTO access.requests (employee_id, resource_id, status, idempotency_key, created_at)
VALUES ($1, $2, $3, gen_random_uuid(), now() - make_interval(mins => $4))`, employee, resource, status, minutesAgo)
}

type statusItem struct {
	ResourceID      string `json:"resource_id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Elevated        bool   `json:"elevated"`
	Status          string   `json:"status"`
	ChecklistItemID string   `json:"checklist_item_id"`
	Environments    []string `json:"environments"`
	Levels          []string `json:"levels"`
	Optional        bool     `json:"optional"`
	ApproverLabel   string   `json:"approver_label"`
	Environment     string   `json:"environment"`
	Level           string   `json:"level"`
}

type statusList struct {
	Items []statusItem `json:"items"`
}

func (l statusList) byID() map[string]statusItem {
	m := map[string]statusItem{}
	for _, it := range l.Items {
		m[it.ResourceID] = it
	}
	return m
}

type result struct {
	ID              string  `json:"id"`
	ResourceID      string  `json:"resource_id"`
	ResourceName    string  `json:"resource_name"`
	Status          string  `json:"status"`
	ApproverID      *string `json:"approver_id"`
	ChecklistItemID *string `json:"checklist_item_id"`
	Environment     *string `json:"environment"`
	Level           string  `json:"level"`
	CreatedAt       string  `json:"created_at"`
	Replayed        bool    `json:"replayed"`
}
