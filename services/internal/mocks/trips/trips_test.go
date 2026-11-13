package trips

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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
	// monday — понедельник 2026-11-09 12:00 по Москве: самое раннее начало командировки — 2026-11-14.
	monday = time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	anna   = mcpx.Caller{Claims: authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}} // Москва, lead.maria
	ivan   = mcpx.Caller{Claims: authn.Claims{Sub: "intern.ivan", Role: "intern", TeamID: "search"}}   // Санкт-Петербург
	maria  = mcpx.Caller{Claims: authn.Claims{Sub: "lead.maria", Role: "lead", TeamID: "delivery"}}    // без руководителя
	petr   = mcpx.Caller{Claims: authn.Claims{Sub: "alumni.petr", Role: "alumni", TeamID: "delivery"}}
)

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	srv  *mcpx.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := pgtest.New(t)
	srv, err := mcpx.NewServer("trips", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret,
		Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	if err := register(srv, pool, func() time.Time { return monday }); err != nil {
		t.Fatal(err)
	}
	if err := srv.Validate(); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, pool: pool, srv: srv}
}

func (e *env) call(c mcpx.Caller, tool string, args any) (json.RawMessage, *apierr.Error) {
	e.t.Helper()
	return e.srv.Invoke(context.Background(), c, tool, args)
}

func must[T any](e *env, c mcpx.Caller, tool string, args any) T {
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

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
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

func confirmed(t *testing.T, c mcpx.Caller, key string, args map[string]any) map[string]any {
	t.Helper()
	out, err := mcpx.WithConfirmation(confSecret, c.Sub, key, args, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func trip(city, start, end, purpose string) map[string]any {
	return map[string]any{"city": city, "start_date": start, "end_date": end, "purpose": purpose}
}

type validation struct {
	Valid               bool     `json:"valid"`
	Problems            []string `json:"problems"`
	ApproverID          string   `json:"approver_id"`
	ApproverDisplayName string   `json:"approver_display_name"`
}

func TestGetPolicy_Trips(t *testing.T) {
	e := newEnv(t)
	p := must[policy](e, anna, "get_policy", nil)
	if len(p.Offices) != 5 || p.MinNoticeDays != 5 || p.MaxDays != 14 || !strings.Contains(p.Text, "тестовые") {
		t.Fatalf("policy %+v", p)
	}
	e.wantCode(petr, "get_policy", nil, apierr.CodeForbidden) // роли: стажёр, наставник, руководитель
}

func TestValidateRequest_Trips_Rules(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name string
		c    mcpx.Caller
		args map[string]any
		want string // подстрока проблемы; пусто — корректно
	}{
		{"valid", anna, trip("Санкт-Петербург", "2026-11-16", "2026-11-18", "Знакомство с командой"), ""},
		{"unknown office", anna, trip("Сочи", "2026-11-16", "2026-11-18", "Отдых"), "Офиса в этом городе нет"},
		{"own city", ivan, trip("Санкт-Петербург", "2026-11-16", "2026-11-18", "Встреча"), "Это ваш город"},
		{"too soon", anna, trip("Казань", "2026-11-10", "2026-11-11", "Встреча"), "не позже чем за 5 дней"},
		{"too long", anna, trip("Казань", "2026-11-16", "2026-12-05", "Проект"), "не больше 14 дней"},
		{"end before start", anna, trip("Казань", "2026-11-18", "2026-11-16", "Проект"), "раньше даты начала"},
		{"no purpose", anna, trip("Казань", "2026-11-16", "2026-11-17", "   "), "цель"},
		{"no lead", maria, trip("Казань", "2026-11-16", "2026-11-17", "Проект"), "согласующего"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := must[validation](e, tt.c, "validate_request", tt.args)
			if tt.want == "" {
				if !got.Valid || got.ApproverID != "lead.maria" || got.ApproverDisplayName != "Мария Лебедева" {
					t.Fatalf("got %+v", got)
				}
				return
			}
			if got.Valid || !strings.Contains(strings.Join(got.Problems, "; "), tt.want) {
				t.Fatalf("got %+v, want problem %q", got, tt.want)
			}
		})
	}
	e.wantCode(anna, "validate_request", trip("Казань", "16.11.2026", "2026-11-17", "Проект"), apierr.CodeValidation)
}

func TestCreateRequest_Trips_ApprovalByLead_Idempotent_R66_R79(t *testing.T) {
	e := newEnv(t)
	key := newKey(t)
	args := confirmed(t, anna, key, trip("Санкт-Петербург", "2026-11-16", "2026-11-18", "Знакомство с командой"))
	got := must[request](e, anna, "create_request", args)
	if !strings.HasPrefix(got.ID, "TRP-") || got.Status != "pending_approval" || got.ApproverID != "lead.maria" ||
		got.ApproverDisplayName != "Мария Лебедева" || got.Replayed == nil || *got.Replayed {
		t.Fatalf("got %+v", got)
	}
	if n := e.count(`SELECT count(*) FROM approvals.items WHERE kind = 'trip_request' AND ref_id = $1 AND approver_id = 'lead.maria'
AND summary = 'Командировка: Санкт-Петербург, 16.11.2026–18.11.2026'`, got.ID); n != 1 {
		t.Fatal("approval item missing")
	}
	again := must[request](e, anna, "create_request", args)
	if again.ID != got.ID || again.Replayed == nil || !*again.Replayed {
		t.Fatalf("replay %+v", again)
	}
	other := confirmed(t, anna, key, trip("Казань", "2026-11-16", "2026-11-18", "Знакомство с командой"))
	e.wantCode(anna, "create_request", other, apierr.CodeConflict)
	overlap := confirmed(t, anna, newKey(t), trip("Казань", "2026-11-17", "2026-11-19", "Проект"))
	terr := e.wantCode(anna, "create_request", overlap, apierr.CodeValidation)
	if !strings.Contains(terr.Message, got.ID) {
		t.Fatalf("overlap message %q", terr.Message)
	}
	list := must[listOut](e, anna, "list_my_requests", nil)
	if len(list.Items) != 1 || list.Items[0].ID != got.ID || len(must[listOut](e, ivan, "list_my_requests", nil).Items) != 0 {
		t.Fatalf("list %+v", list)
	}
	e.wantCode(anna, "create_request", trip("Казань", "2026-11-20", "2026-11-21", "Проект"), apierr.CodeInvalidConfirmation) // без подтверждения
}

func TestRegister_Trips_Errors(t *testing.T) {
	if err := register(nil, nil, nil); err == nil {
		t.Fatal("nil server accepted")
	}
	srv, _ := mcpx.NewServer("absence", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err := Register(srv, &pgxpool.Pool{}); err == nil {
		t.Fatal("wrong server accepted")
	}
	if period(monday, monday) != "09.11.2026" || deref(nil) != "" {
		t.Fatal("helpers")
	}
}
