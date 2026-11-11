package mcpx

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// rawPost sends one JSON-RPC message the way the Python MCP SDK (streamablehttp_client) does.
func rawPost(t *testing.T, url, token, protocol string, msg map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(msg)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if protocol != "" {
		req.Header.Set("mcp-protocol-version", protocol)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("response is not JSON: %s", raw)
		}
	}
	return resp, out
}

func dig(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[p]
	}
	return cur
}

// A non-Go client (the Python agent) speaks plain JSON-RPC over HTTP: initialize, initialized, tools/list,
// tools/call — all answered as application/json without a session id.
func TestRawJSONRPC_PythonStyleClient_R71(t *testing.T) {
	ts, _ := stack(t, newAbsence(t, nil, &recorder{}))
	url := ts.URL + "/mcp/absence"
	tok := jwtFor(t, "intern.anna", "intern", "delivery")

	resp, init := rawPost(t, url, tok, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "mcp", "version": "1.12.0"},
		},
	})
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("initialize: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Mcp-Session-Id") != "" {
		t.Fatal("stateless server must not assign a session id")
	}
	if dig(init, "result", "protocolVersion") != "2025-06-18" || dig(init, "result", "serverInfo", "name") != "aibuddy-absence" ||
		dig(init, "result", "capabilities", "tools") == nil {
		t.Fatalf("initialize result %v", init)
	}

	resp, _ = rawPost(t, url, tok, "2025-06-18", map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("initialized: %d", resp.StatusCode)
	}

	_, list := rawPost(t, url, tok, "2025-06-18", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	tools, _ := dig(list, "result", "tools").([]any)
	var names []string
	for _, x := range tools {
		tool := x.(map[string]any)
		names = append(names, tool["name"].(string))
		if dig(tool, "inputSchema", "additionalProperties") != false {
			t.Fatalf("contract inputSchema not served as is: %v", tool["inputSchema"])
		}
	}
	if strings.Join(names, ",") != "create_request,get_policy,get_request,list_my_requests,reschedule_request,validate_request" { // go-sdk sorts by name
		t.Fatalf("tools %v", names)
	}

	args := confirmed(t, "intern.anna", createArgs())
	_, call := rawPost(t, url, tok, "2025-06-18", map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "create_request", "arguments": args},
	})
	if dig(call, "result", "isError") == true || dig(call, "result", "structuredContent", "id") != "ABS-000001" {
		t.Fatalf("tools/call %v", call)
	}
	content, _ := dig(call, "result", "content").([]any)
	if len(content) != 1 || dig(content[0].(map[string]any), "type") != "text" {
		t.Fatalf("content %v", content)
	}
	var fromText map[string]any
	if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &fromText); err != nil || fromText["id"] != "ABS-000001" {
		t.Fatalf("text content must carry the same JSON: %v", content)
	}

	// Tool error, and a call without any initialize (pure stateless).
	_, denied := rawPost(t, url, tok, "2025-06-18", map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "get_request", "arguments": map[string]any{"id": "ABS-000777"}},
	})
	if dig(denied, "result", "isError") != true || dig(denied, "result", "structuredContent", "code") != "forbidden" ||
		dig(denied, "result", "structuredContent", "retryable") != false {
		t.Fatalf("denied %v", denied)
	}

	// Unknown tool is a JSON-RPC error, not a tool result.
	_, unknown := rawPost(t, url, tok, "2025-06-18", map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{"name": "drop_tables"},
	})
	if dig(unknown, "error", "code") == nil {
		t.Fatalf("unknown tool %v", unknown)
	}
}

func TestRawJSONRPC_Unauthenticated_R71(t *testing.T) {
	ts, _ := stack(t, newAbsence(t, nil, &recorder{}))
	url := ts.URL + "/mcp/absence"
	ping := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	for name, tok := range map[string]string{
		"missing":      "",
		"garbage":      "not.a.jwt",
		"wrong secret": func() string { s := jwtFor(t, "intern.anna", "intern", "delivery"); return s[:len(s)-2] + "xx" }(),
	} {
		t.Run(name, func(t *testing.T) {
			resp, body := rawPost(t, url, tok, "", ping)
			if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if dig(body, "jsonrpc") != "2.0" || dig(body, "error", "code") != float64(rpcErrUnauthorized) ||
				dig(body, "error", "data", "code") != "unauthorized" {
				t.Fatalf("body %v", body)
			}
		})
	}
}

func TestRawJSONRPC_TransportRules(t *testing.T) {
	ts, _ := stack(t, newAbsence(t, nil, &recorder{}))
	url := ts.URL + "/mcp/absence"
	tok := jwtFor(t, "intern.anna", "intern", "delivery")
	// The go-sdk requires both media types in Accept (the Python SDK sends both).
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Accept json only: %d", resp.StatusCode)
	}
	// Body limit.
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"x":"` + strings.Repeat("a", MaxRequestBody) + `"}}`
	req, _ = http.NewRequest(http.MethodPost, url, strings.NewReader(big))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Fatalf("oversized body accepted: %d", resp.StatusCode)
	}
}
