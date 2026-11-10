package mcpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/httpx"
)

// Client calls tools of the mock MCP servers on behalf of a user (core: approvals.*, kb.add_faq; tests).
// The zero value is not usable: set BaseURL. Safe for concurrent use.
type Client struct {
	BaseURL    string       // e.g. http://mcp-mocks:8100/mcp; the server name is appended as /<server>
	HTTPClient *http.Client // nil means a client with http.DefaultTransport and no timeout (use ctx deadlines)
}

// Call opens a session to BaseURL/<server> with `Authorization: Bearer userToken`, calls tool with args (any
// JSON-object value; nil means {}), closes the session and returns exactly one of:
//   - the structured result (JSON object) on success;
//   - a tool error (*apierr.Error with an MCP code: forbidden, not_found, validation_error, conflict,
//     invalid_confirmation, unavailable);
//   - an error for transport and protocol failures. That error wraps an *apierr.Error (find it with apierr.As):
//     unauthorized when the server rejected the token, internal for protocol errors (e.g. unknown tool),
//     upstream_unavailable otherwise. It also wraps the cause (context errors keep working with errors.Is).
//
// X-Request-Id from httpx.RequestIDFromContext(ctx) is forwarded.
func (c *Client) Call(ctx context.Context, userToken, server, tool string, args any) (json.RawMessage, *apierr.Error, error) {
	rt := &bearerTransport{token: userToken, base: http.DefaultTransport}
	hc := &http.Client{Transport: rt}
	if c.HTTPClient != nil {
		if c.HTTPClient.Transport != nil {
			rt.base = c.HTTPClient.Transport
		}
		hc.Timeout = c.HTTPClient.Timeout
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:   strings.TrimRight(c.BaseURL, "/") + "/" + server,
		HTTPClient: hc,
		MaxRetries: -1,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "aibuddy-core", Version: version}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, rt.wrap(ctx, fmt.Sprintf("connect %s", server), err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return nil, nil, rt.wrap(ctx, fmt.Sprintf("call %s.%s", server, tool), err)
	}
	if res.IsError {
		return nil, toolError(res), nil
	}
	out, err := structured(res)
	if err != nil {
		return nil, nil, fmt.Errorf("mcpx: call %s.%s: %w: %w", server, tool, apierr.Internal(), err)
	}
	return out, nil, nil
}

// structured returns the structured content of a successful result, falling back to JSON text content.
func structured(res *mcp.CallToolResult) (json.RawMessage, error) {
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return nil, fmt.Errorf("encode structured content: %w", err)
		}
		return raw, nil
	}
	if text := firstText(res); json.Valid([]byte(text)) && strings.HasPrefix(strings.TrimSpace(text), "{") {
		return json.RawMessage(text), nil
	}
	return nil, errors.New("result has no structured content")
}

// toolError decodes {code, message, retryable} from an isError result; unknown shapes become unavailable.
func toolError(res *mcp.CallToolResult) *apierr.Error {
	var wire struct {
		Code      apierr.Code `json:"code"`
		Message   string      `json:"message"`
		Retryable bool        `json:"retryable"`
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if res.StructuredContent == nil {
		raw = []byte(firstText(res))
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Code == "" {
		e := apierr.Unavailable()
		if text := firstText(res); text != "" {
			e = e.WithMessage(text)
		}
		e.Retryable = false
		return e
	}
	e := apierr.New(wire.Code, wire.Message)
	e.Retryable = wire.Retryable
	return e
}

func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

// bearerTransport adds the user's token and the request id to every request of a session and remembers
// whether any HTTP exchange failed, to classify errors.
type bearerTransport struct {
	token string
	base  http.RoundTripper

	mu           sync.Mutex
	unauthorized bool
	httpFailed   bool
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	if id := httpx.RequestIDFromContext(req.Context()); id != "" {
		r.Header.Set(httpx.HeaderRequestID, id)
	}
	resp, err := t.base.RoundTrip(r)
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case err != nil:
		t.httpFailed = true
	case resp.StatusCode == http.StatusUnauthorized:
		t.unauthorized = true
	case resp.StatusCode >= 400 && !(req.Method == http.MethodGet && resp.StatusCode == http.StatusMethodNotAllowed) &&
		req.Method != http.MethodDelete:
		t.httpFailed = true // a 405 for the optional hanging GET and the DELETE on close are expected
	}
	return resp, err
}

func (t *bearerTransport) wrap(ctx context.Context, op string, err error) error {
	t.mu.Lock()
	unauthorized, httpFailed := t.unauthorized, t.httpFailed
	t.mu.Unlock()
	kind := apierr.UpstreamUnavailable()
	switch {
	case unauthorized:
		kind = apierr.Unauthorized()
	case !httpFailed && ctx.Err() == nil:
		kind = apierr.Internal() // the server answered with a JSON-RPC error
	}
	if cerr := ctx.Err(); cerr != nil && !errors.Is(err, cerr) {
		err = fmt.Errorf("%w: %w", cerr, err)
	}
	return fmt.Errorf("mcpx: %s: %w: %w", op, kind, err)
}
