package approvals

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
	anna   = caller("intern.anna", "intern", "delivery") // lead: lead.maria
	ivan   = caller("intern.ivan", "intern", "search")   // lead: lead.dmitry
	oleg   = caller("mentor.oleg", "mentor", "delivery") // lead: lead.maria
	maria  = caller("lead.maria", "lead", "delivery")
	dmitry = caller("lead.dmitry", "lead", "search")
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
	return &env{t: t, pool: pool, srv: newServer(t, "approvals", pool, Register)}
}

func newServer(t *testing.T, name string, pool *pgxpool.Pool, register func(*mcpx.Server, *pgxpool.Pool) error) *mcpx.Server {
	t.Helper()
	srv, err := mcpx.NewServer(name, mcpx.Config{
		JWTSecret: jwtSecret, ConfirmationSecret: confSecret, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := register(srv, pool); err != nil {
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

func decision(id, d string) map[string]any {
	return map[string]any{"approval_id": id, "decision": d}
}

func (e *env) scalar(dst any, sql string, args ...any) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(dst); err != nil {
		e.t.Fatalf("query %q: %v", sql, err)
	}
}

func (e *env) str(sql string, args ...any) string {
	e.t.Helper()
	var s string
	e.scalar(&s, sql, args...)
	return s
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	e.scalar(&n, sql, args...)
	return n
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("exec %q: %v", sql, err)
	}
}

// seedAbsence inserts a pending day-off request of employee and its approval item for approver.
func (e *env) seedAbsence(employee, approver string) (requestID, itemID string) {
	e.t.Helper()
	requestID = e.str(`INSERT INTO absence.requests (employee_id, type, start_date, end_date, approver_id, idempotency_key)
VALUES ($1, 'day_off', '2026-11-10', '2026-11-10', $2, gen_random_uuid()) RETURNING id`, employee, approver)
	itemID = e.str(`INSERT INTO approvals.items (kind, ref_id, requester_id, approver_id, summary)
VALUES ('absence_request', $1, $2, $3, 'Отгул 10.11.2026') RETURNING id`, requestID, employee, approver)
	return requestID, itemID
}

// seedAccess inserts a pending access request of employee and its approval item for approver.
func (e *env) seedAccess(employee, approver, resource string) (requestID, itemID string) {
	e.t.Helper()
	requestID = e.str(`INSERT INTO access.requests (employee_id, resource_id, status, approver_id, idempotency_key)
VALUES ($1, $3, 'pending_approval', $2, gen_random_uuid()) RETURNING id`, employee, approver, resource)
	itemID = e.str(`INSERT INTO approvals.items (kind, ref_id, requester_id, approver_id, summary)
SELECT 'access_request', $1, $2, $3, 'Доступ: ' || name FROM access.resources WHERE id = $4 RETURNING id`,
		requestID, employee, approver, resource)
	return requestID, itemID
}

type itemOut struct {
	ID                   string `json:"id"`
	Kind                 string `json:"kind"`
	RefID                string `json:"ref_id"`
	RequesterID          string `json:"requester_id"`
	RequesterDisplayName string `json:"requester_display_name"`
	Summary              string `json:"summary"`
	Status               string `json:"status"`
	Comment              string `json:"comment"`
	CreatedAt            string `json:"created_at"`
	Replayed             *bool  `json:"replayed"`
}

type listResult struct {
	Items []itemOut `json:"items"`
}
