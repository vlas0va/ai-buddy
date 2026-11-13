package mcpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/httpx"
)

func TestClient_TransportErrors(t *testing.T) {
	ts, client := stack(t, newAbsence(t, nil, &recorder{}))
	tok := jwtFor(t, "intern.anna", "intern", "delivery")

	t.Run("bad token is unauthorized", func(t *testing.T) {
		_, terr, err := client.Call(context.Background(), "bad", "absence", "get_policy", nil)
		if terr != nil || !apierr.HasCode(err, apierr.CodeUnauthorized) {
			t.Fatalf("got %v / %v", terr, err)
		}
	})
	t.Run("unknown tool is internal", func(t *testing.T) {
		_, terr, err := client.Call(context.Background(), tok, "absence", "drop_tables", nil)
		if terr != nil || !apierr.HasCode(err, apierr.CodeInternal) {
			t.Fatalf("got %v / %v", terr, err)
		}
	})
	t.Run("unknown server is upstream_unavailable", func(t *testing.T) {
		_, _, err := client.Call(context.Background(), tok, "payroll", "get_salary", nil)
		if !apierr.HasCode(err, apierr.CodeUpstreamUnavailable) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("server down is upstream_unavailable", func(t *testing.T) {
		down := httptest.NewServer(http.NotFoundHandler())
		down.Close()
		_, _, err := (&Client{BaseURL: down.URL + "/mcp"}).Call(context.Background(), tok, "absence", "get_policy", nil)
		e, ok := apierr.As(err)
		if !ok || e.Code != apierr.CodeUpstreamUnavailable || !e.Retryable {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := client.Call(ctx, tok, "absence", "get_policy", nil)
		if !errors.Is(err, context.Canceled) || !apierr.HasCode(err, apierr.CodeUpstreamUnavailable) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("custom HTTP client keeps its transport and timeout", func(t *testing.T) {
		var mu sync.Mutex
		seen := 0
		hc := &http.Client{Timeout: 5 * time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			seen++
			mu.Unlock()
			return http.DefaultTransport.RoundTrip(r)
		})}
		c := &Client{BaseURL: ts.URL + "/mcp", HTTPClient: hc}
		if _, terr, err := c.Call(context.Background(), tok, "absence", "get_policy", nil); err != nil || terr != nil {
			t.Fatalf("got %v / %v", terr, err)
		}
		mu.Lock()
		defer mu.Unlock()
		if seen == 0 {
			t.Fatal("custom transport not used")
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClient_ForwardsTokenAndRequestID(t *testing.T) {
	s := newAbsence(t, nil, &recorder{})
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var auths, ids []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		ids = append(ids, r.Header.Get(httpx.HeaderRequestID))
		mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	defer ts.Close()
	tok := jwtFor(t, "intern.anna", "intern", "delivery")
	ctx := httpx.WithRequestID(context.Background(), "req-42")
	if _, terr, err := (&Client{BaseURL: ts.URL}).Call(ctx, tok, "", "get_policy", nil); err != nil || terr != nil {
		t.Fatalf("got %v / %v", terr, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(auths) < 3 {
		t.Fatalf("requests %d", len(auths))
	}
	for i := range auths {
		if auths[i] != "Bearer "+tok {
			t.Fatalf("request %d without the user token", i)
		}
		if i < 2 && ids[i] != "req-42" { // initialize и initialized несут контекст вызывающего
			t.Fatalf("request %d: request id %q", i, ids[i])
		}
	}
}

func TestClientResultDecoding(t *testing.T) {
	text := func(s string) []mcp.Content { return []mcp.Content{&mcp.TextContent{Text: s}} }
	tests := []struct {
		name string
		res  *mcp.CallToolResult
		code apierr.Code
		msg  string
	}{
		{"structured", &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"code": "conflict", "message": "Уже", "retryable": true}}, apierr.CodeConflict, "Уже"},
		{"text JSON only", &mcp.CallToolResult{IsError: true, Content: text(`{"code":"not_found","message":"Нет","retryable":false}`)}, apierr.CodeNotFound, "Нет"},
		{"plain text", &mcp.CallToolResult{IsError: true, Content: text("something broke")}, apierr.CodeUnavailable, "something broke"},
		{"empty", &mcp.CallToolResult{IsError: true}, apierr.CodeUnavailable, apierr.Unavailable().Message},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := toolError(tt.res)
			if e.Code != tt.code || e.Message != tt.msg {
				t.Fatalf("got %+v", e)
			}
		})
	}
	if e := toolError(tests[0].res); !e.Retryable {
		t.Fatal("retryable must come from the wire")
	}
	if e := toolError(tests[2].res); e.Retryable {
		t.Fatal("unknown error shape must not be retryable")
	}
	if out, err := structured(&mcp.CallToolResult{Content: text(`{"a":1}`)}); err != nil || string(out) != `{"a":1}` {
		t.Fatalf("text fallback: %s %v", out, err)
	}
	if _, err := structured(&mcp.CallToolResult{Content: text("ok")}); err == nil {
		t.Fatal("non-JSON success accepted")
	}
	if _, err := structured(&mcp.CallToolResult{StructuredContent: map[string]any{"x": make(chan int)}}); err == nil {
		t.Fatal("unencodable structured content accepted")
	}
}

// Сервер отвечает успехом без структурированного содержимого: Call сообщает internal.
func TestClient_SuccessWithoutStructuredContent(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "odd", Version: "1"}, &mcp.ServerOptions{GetSessionID: func() string { return "" }})
	srv.AddTool(&mcp.Tool{Name: "hello", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "hi"}}}, nil
		})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	ts := httptest.NewServer(h)
	defer ts.Close()
	_, terr, err := (&Client{BaseURL: ts.URL}).Call(context.Background(), "t", "odd", "hello", nil)
	if terr != nil || !apierr.HasCode(err, apierr.CodeInternal) {
		t.Fatalf("got %v / %v", terr, err)
	}
}

func TestStartupValidation(t *testing.T) {
	t.Run("missing handler, unknown tool, duplicate, nil", func(t *testing.T) {
		s, err := NewServer("approvals", Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
		if err != nil {
			t.Fatal(err)
		}
		h := func(context.Context, Caller, json.RawMessage) (any, error) { return map[string]any{}, nil }
		s.Handle("list_pending", h)
		s.Handle("list_pending", h)
		s.Handle("get_salary", h)
		s.Handle("decide", nil)
		err = s.Validate()
		for _, want := range []string{`"list_pending" registered twice`, `"get_salary" is not in the contract`, `nil handler for "decide"`, `no handler for contract tool "decide"`} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("Validate lacks %q: %v", want, err)
			}
		}
		if _, err := s.Handler(); err == nil {
			t.Error("Handler must refuse an invalid server")
		}
		if err := Mount(http.NewServeMux(), s); err == nil {
			t.Error("Mount must refuse an invalid server")
		}
	})
	t.Run("secrets", func(t *testing.T) {
		s, _ := NewServer("approvals", Config{})
		err := s.Validate()
		if err == nil || !strings.Contains(err.Error(), "empty JWT secret") || !strings.Contains(err.Error(), "empty confirmation secret") {
			t.Fatalf("got %v", err)
		}
		r, _ := NewServer("directory", Config{JWTSecret: jwtSecret})
		r.Handle("whoami", func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil })
		r.Handle("get_my_contacts", func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil })
		if err := r.Validate(); err != nil {
			t.Fatalf("read-only server needs no confirmation secret: %v", err)
		}
	})
	t.Run("every contract loads", func(t *testing.T) {
		for _, name := range []string{"absence", "approvals", "access", "checklist", "directory", "kb"} {
			if _, err := NewServer(name, Config{}); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
		if _, err := NewServer("payroll", Config{}); err == nil {
			t.Error("unknown server accepted")
		}
	})
	obj := json.RawMessage(`{"type":"object"}`)
	writeIn := json.RawMessage(`{"type":"object","properties":{"idempotency_key":{"type":"string"},"confirmation_token":{"type":"string"}}}`)
	bad := []struct {
		name string
		srv  string
		defs []ToolDef
	}{
		{"server name", "a/b", nil},
		{"no tool name", "x", []ToolDef{{Roles: []string{"intern"}, InputSchema: obj}}},
		{"no roles", "x", []ToolDef{{Name: "t", InputSchema: obj}}},
		{"input not object", "x", []ToolDef{{Name: "t", Roles: []string{"intern"}, InputSchema: json.RawMessage(`{"type":"string"}`)}}},
		{"input not JSON", "x", []ToolDef{{Name: "t", Roles: []string{"intern"}, InputSchema: json.RawMessage(`{`)}}},
		{"input unresolvable", "x", []ToolDef{{Name: "t", Roles: []string{"intern"}, InputSchema: json.RawMessage(`{"type":"object","$ref":"https://example.com/x.json"}`)}}},
		{"write without confirmation fields", "x", []ToolDef{{Name: "t", Roles: []string{"intern"}, Write: true, InputSchema: obj}}},
		{"output not object", "x", []ToolDef{{Name: "t", Roles: []string{"intern"}, Write: true, InputSchema: writeIn, OutputSchema: json.RawMessage(`{"type":"array"}`)}}},
		{"duplicate tool", "x", []ToolDef{{Name: "t", Roles: []string{"intern"}, InputSchema: obj}, {Name: "t", Roles: []string{"intern"}, InputSchema: obj}}},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newServer(tt.srv, tt.defs, Config{}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	fsys := fstest.MapFS{
		"schemas/broken.json": {Data: []byte(`{`)},
		"schemas/liar.json":   {Data: []byte(`{"server":"other","tools":[]}`)},
	}
	for _, name := range []string{"broken", "liar", "absent"} {
		if _, err := loadTools(fsys, name); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestCallerPlumbing(t *testing.T) {
	if _, ok := CallerFrom(context.Background(), nil); ok {
		t.Fatal("no caller expected")
	}
	if _, err := tokenInfo(context.Background(), "x", nil); err == nil {
		t.Fatal("tokenInfo without caller must fail")
	}
	// tools/call, каким-то образом дошедший до обработчика без личности, отклоняется.
	s := newAbsence(t, nil, &recorder{})
	res, err := s.toolHandler(s.tools["get_policy"])(context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "get_policy"}})
	if err != nil || !res.IsError || !strings.Contains(string(res.StructuredContent.(json.RawMessage)), `"forbidden"`) {
		t.Fatalf("got %+v %v", res, err)
	}
}
