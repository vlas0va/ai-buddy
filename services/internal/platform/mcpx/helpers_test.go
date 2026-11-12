package mcpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
)

var (
	jwtSecret  = []byte("test-jwt-secret")
	confSecret = []byte("test-confirm-secret")
)

const (
	proposalID = "6f1c2b9e-3d4a-4b5c-8d7e-0f1a2b3c4d5e"
	secretErr  = "pq: password authentication failed for user buddy"
)

func jwtFor(t *testing.T, sub, role, team string) string {
	t.Helper()
	tok, err := authn.Issue(jwtSecret, authn.Claims{Sub: sub, Role: role, TeamID: team, CohortStart: "2026-11-02"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// syncBuffer is a log sink safe for the server goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// recorder keeps the arguments the write handler received.
type recorder struct {
	mu    sync.Mutex
	calls []json.RawMessage
}

func (r *recorder) add(a json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, a)
}

func (r *recorder) n() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func absenceRequest(id string) map[string]any {
	return map[string]any{
		"id": id, "type": "day_off", "start_date": "2026-11-14", "end_date": "2026-11-14",
		"status": "pending_approval", "approver_id": "lead.maria", "created_at": "2026-11-09T10:00:00Z", "replayed": false,
	}
}

// newAbsence binds test handlers to the real absence contract.
func newAbsence(t *testing.T, log *slog.Logger, writes *recorder) *Server {
	t.Helper()
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s, err := NewServer("absence", Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	s.Handle("get_policy", func(ctx context.Context, c Caller, _ json.RawMessage) (any, error) {
		ctxClaims, _ := authn.FromContext(ctx)
		return map[string]any{
			"doc_id": "absence-policy-" + c.TeamID, "title": "Тестовая инструкция прототипа", "source_type": "test",
			"text": c.Sub + "|" + c.Role + "|" + c.TeamID + "|" + c.CohortStart + "|token=" + boolStr(c.Token != "") +
				"|ctx=" + ctxClaims.Sub,
		}, nil
	})
	s.Handle("list_my_requests", func(_ context.Context, _ Caller, args json.RawMessage) (any, error) {
		var in struct {
			Status string `json:"status"`
		}
		if err := DecodeArgs(args, &in); err != nil {
			return nil, err
		}
		if in.Status == "cancelled" {
			return nil, errors.New(secretErr)
		}
		return map[string]any{"items": []any{}}, nil
	})
	s.Handle("validate_request", func(_ context.Context, _ Caller, args json.RawMessage) (any, error) {
		var in struct {
			StartDate string `json:"start_date"`
		}
		_ = DecodeArgs(args, &in)
		switch in.StartDate {
		case "2026-01-01":
			panic("boom")
		case "2026-01-02":
			return map[string]any{"valid": "yes"}, nil // violates outputSchema
		case "2026-01-03":
			return []int{1}, nil // not an object
		case "2026-01-04":
			return map[string]any{"bad": make(chan int)}, nil // not encodable
		}
		return map[string]any{"valid": true, "problems": []string{}}, nil
	})
	s.Handle("create_request", func(_ context.Context, _ Caller, args json.RawMessage) (any, error) {
		writes.add(args)
		return absenceRequest("ABS-000001"), nil
	})
	s.Handle("reschedule_request", func(_ context.Context, _ Caller, args json.RawMessage) (any, error) {
		writes.add(args)
		return absenceRequest("ABS-000001"), nil
	})
	s.Handle("cancel_request", func(_ context.Context, _ Caller, args json.RawMessage) (any, error) {
		writes.add(args)
		return absenceRequest("ABS-000001"), nil
	})
	s.Handle("get_request", func(ctx context.Context, c Caller, args json.RawMessage) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		_ = DecodeArgs(args, &in)
		switch in.ID {
		case "ABS-000001":
			return absenceRequest(in.ID), nil
		case "ABS-000409":
			return nil, apierr.Conflict().WithMessage("Заявка уже рассмотрена")
		case "ABS-000429":
			return nil, apierr.RateLimited()
		case "ABS-000404":
			return nil, errors.Join(errors.New("ctx"), apierr.NotFound())
		case "ABS-000499":
			return nil, context.Canceled
		}
		return nil, apierr.Forbidden()
	})
	return s
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func newApprovals(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer("approvals", Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	s.Handle("list_pending", func(context.Context, Caller, json.RawMessage) (any, error) {
		return map[string]any{"items": []any{}}, nil
	})
	s.Handle("decide", func(context.Context, Caller, json.RawMessage) (any, error) {
		return map[string]any{"id": "a1", "kind": "absence_request", "ref_id": "ABS-000001", "requester_id": "intern.anna",
			"summary": "Отгул 14.11.2026", "status": "approved", "created_at": "2026-11-09T10:00:00Z"}, nil
	})
	return s
}

// stack mounts servers on a test HTTP server and returns it with a client.
func stack(t *testing.T, servers ...*Server) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	if err := Mount(mux, servers...); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &Client{BaseURL: ts.URL + "/mcp/"}
}

// session opens a go-sdk client session with a bearer token (for tools/list).
func session(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	hc := &http.Client{Transport: &bearerTransport{token: token, base: http.DefaultTransport}}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: hc, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}
