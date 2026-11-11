package consents

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
	anna       = mcpx.Caller{Claims: authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}}
	ivan       = mcpx.Caller{Claims: authn.Claims{Sub: "intern.ivan", Role: "intern", TeamID: "search"}}
)

type env struct {
	t   *testing.T
	srv *mcpx.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := pgtest.New(t)
	srv, err := mcpx.NewServer("consents", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret,
		Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(srv, pool); err != nil {
		t.Fatal(err)
	}
	if err := srv.Validate(); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, srv: srv}
}

func must[T any](e *env, c mcpx.Caller, tool string, args any) T {
	e.t.Helper()
	raw, terr := e.srv.Invoke(context.Background(), c, tool, args)
	if terr != nil {
		e.t.Fatalf("%s: %v", tool, terr)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) wantCode(c mcpx.Caller, tool string, args any, code apierr.Code) *apierr.Error {
	e.t.Helper()
	raw, terr := e.srv.Invoke(context.Background(), c, tool, args)
	if terr == nil || terr.Code != code {
		e.t.Fatalf("%s: want %s, got %v / %s", tool, code, terr, raw)
	}
	return terr
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

func byID(l listOut) map[string]consent {
	m := map[string]consent{}
	for _, c := range l.Items {
		m[c.ConsentID] = c
	}
	return m
}

func TestList_Consents_HRPortalSignedAtHire(t *testing.T) {
	e := newEnv(t)
	m := byID(must[listOut](e, anna, "list", nil))
	if len(m) != 4 || m["pdn"].Status != "signed" || m["pdn"].SignedVia != "hr_portal" || m["pdn"].Signable ||
		!m["nda"].Required || m["photo"].Status != "not_signed" || !m["photo"].Signable || m["surveys"].Required {
		t.Fatalf("consents %+v", m)
	}
}

func TestSign_Consents_VoluntaryOnly_Idempotent_R71_R79(t *testing.T) {
	e := newEnv(t)
	key := newKey(t)
	args := confirmed(t, anna, key, map[string]any{"consent_id": "photo"})
	got := must[signature](e, anna, "sign", args)
	if !strings.HasPrefix(got.ID, "CNS-") || got.Status != "signed" || got.Title == "" || got.Replayed == nil || *got.Replayed {
		t.Fatalf("got %+v", got)
	}
	again := must[signature](e, anna, "sign", args)
	if again.ID != got.ID || again.Replayed == nil || !*again.Replayed {
		t.Fatalf("replay %+v", again)
	}
	if byID(must[listOut](e, anna, "list", nil))["photo"].SignedVia != "buddy" ||
		byID(must[listOut](e, ivan, "list", nil))["photo"].Status != "not_signed" {
		t.Fatal("signature must belong to the caller only")
	}
	e.wantCode(anna, "sign", confirmed(t, anna, newKey(t), map[string]any{"consent_id": "photo"}), apierr.CodeConflict)
	e.wantCode(anna, "sign", confirmed(t, anna, key, map[string]any{"consent_id": "surveys"}), apierr.CodeConflict)
	terr := e.wantCode(anna, "sign", confirmed(t, anna, newKey(t), map[string]any{"consent_id": "pdn"}), apierr.CodeForbidden)
	if !strings.Contains(terr.Message, "кадровом портале") {
		t.Fatalf("message %q", terr.Message)
	}
	e.wantCode(anna, "sign", confirmed(t, anna, newKey(t), map[string]any{"consent_id": "nope"}), apierr.CodeNotFound)
	e.wantCode(anna, "sign", map[string]any{"consent_id": "surveys"}, apierr.CodeInvalidConfirmation) // no confirmation
}

func TestRegister_Consents_Errors(t *testing.T) {
	if err := Register(nil, nil); err == nil {
		t.Fatal("nil server accepted")
	}
	srv, _ := mcpx.NewServer("trips", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err := Register(srv, &pgxpool.Pool{}); err == nil {
		t.Fatal("wrong server accepted")
	}
}
