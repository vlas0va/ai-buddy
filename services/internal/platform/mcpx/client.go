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

// Client вызывает инструменты мок-серверов MCP от имени пользователя (ядро: approvals.*, kb.add_faq; тесты). Нулевое
// значение непригодно: задайте BaseURL. Безопасен для конкурентного использования.
type Client struct {
	BaseURL    string       // например http://mcp-mocks:8100/mcp; имя сервера добавляется как /<server>
	HTTPClient *http.Client // nil означает клиента с http.DefaultTransport без таймаута (используйте дедлайны ctx)
}

// Call открывает сессию к BaseURL/<server> с `Authorization: Bearer userToken`, вызывает tool с args (любое
// значение-JSON-объект; nil — {}), закрывает сессию и возвращает ровно одно из:
//   - структурированный результат (JSON-объект) при успехе;
//   - ошибку инструмента (*apierr.Error с кодом MCP: forbidden, not_found, validation_error, conflict,
//     invalid_confirmation, unavailable);
//   - ошибку при сбоях транспорта и протокола. Она оборачивает *apierr.Error (ищите через apierr.As):
//     unauthorized, если сервер отклонил токен, internal для ошибок протокола (например, неизвестный инструмент),
//     иначе upstream_unavailable. Она же оборачивает причину (ошибки контекста работают с errors.Is).
//
// X-Request-Id из httpx.RequestIDFromContext(ctx) передаётся дальше.
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

// structured возвращает структурированное содержимое успешного результата, а если его нет — текстовое содержимое JSON.
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

// toolError декодирует {code, message, retryable} из результата с isError; неизвестные формы становятся unavailable.
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

// bearerTransport добавляет токен пользователя и id запроса к каждому запросу сессии и запоминает, был ли сбой
// какого-либо HTTP-обмена, чтобы классифицировать ошибки.
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
		t.httpFailed = true // 405 на необязательный висящий GET и на DELETE при закрытии ожидаемы
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
		kind = apierr.Internal() // сервер ответил ошибкой JSON-RPC
	}
	if cerr := ctx.Err(); cerr != nil && !errors.Is(err, cerr) {
		err = fmt.Errorf("%w: %w", cerr, err)
	}
	return fmt.Errorf("mcpx: %s: %w: %w", op, kind, err)
}
