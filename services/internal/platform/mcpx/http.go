package mcpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
)

// PathPrefix — где Mount обслуживает серверы: /mcp/<name>.
const PathPrefix = "/mcp/"

// MaxRequestBody ограничивает тело одного HTTP-запроса MCP (1 МиБ).
const MaxRequestBody = 1 << 20

const (
	callerKey = "mcpx.caller" // ключ в auth.TokenInfo.Extra
	version   = "0.1.0"
)

// Mount проверяет каждый сервер и обслуживает его по PathPrefix+name на mux. Падает на первом некорректном сервере.
func Mount(mux *http.ServeMux, servers ...*Server) error {
	for _, s := range servers {
		h, err := s.Handler()
		if err != nil {
			return err
		}
		mux.Handle(PathPrefix+s.name, h)
	}
	return nil
}

// Handler проверяет сервер (см. Validate) и возвращает его HTTP-обработчик: проверка JWT, затем streamable
// HTTP-обработчик go-sdk в режиме без состояния с JSON-ответами (без id сессии, без SSE). Отсутствующий или
// некорректный JWT получает 401 с `WWW-Authenticate: Bearer` и телом ошибки JSON-RPC, в data которого JSON apierr.
func (s *Server) Handler() (http.Handler, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "aibuddy-" + s.name, Version: version}, &mcp.ServerOptions{
		HasTools:     true,
		GetSessionID: func() string { return "" }, // без состояния: без Mcp-Session-Id
	})
	for _, n := range s.order {
		t := s.tools[n]
		mt := &mcp.Tool{Name: t.def.Name, Description: t.def.Description, InputSchema: t.def.InputSchema}
		if len(t.def.OutputSchema) > 0 {
			mt.OutputSchema = t.def.OutputSchema
		}
		srv.AddTool(mt, s.toolHandler(t))
	}
	srv.AddReceivingMiddleware(s.filterToolList)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	// RequireBearerToken — так go-sdk переносит личность: TokenInfo, который он кладёт в контекст запроса,
	// streamable-транспорт копирует в CallToolRequest.Extra.TokenInfo для каждого сообщения JSON-RPC.
	withTokenInfo := auth.RequireBearerToken(tokenInfo, nil)(streamable)
	verifier := authn.NewVerifier(s.cfg.JWTSecret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := authn.BearerToken(r)
		if !ok {
			writeUnauthorized(w)
			return
		}
		claims, err := verifier.Verify(token)
		if err != nil {
			s.log.DebugContext(r.Context(), "mcp jwt rejected", "server", s.name, "err", err)
			writeUnauthorized(w)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBody)
		ctx := authn.WithToken(authn.NewContext(r.Context(), claims), token)
		withTokenInfo.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}

// tokenInfo — это auth.TokenVerifier: JWT уже проверен, поэтому он только оборачивает вызывающего.
func tokenInfo(ctx context.Context, _ string, _ *http.Request) (*auth.TokenInfo, error) {
	c, ok := callerFromContext(ctx)
	if !ok {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{
		Expiration: time.Unix(c.Exp, 0).Add(authn.DefaultLeeway),
		Extra:      map[string]any{callerKey: c},
	}, nil
}

func callerFromContext(ctx context.Context) (Caller, bool) {
	claims, ok := authn.FromContext(ctx)
	if !ok {
		return Caller{}, false
	}
	token, _ := authn.TokenFromContext(ctx)
	return Caller{Claims: claims, Token: token}, true
}

// CallerFrom возвращает вызывающего MCP-запроса: из req.Extra.TokenInfo (ставит Handler), а если его нет — из
// контекста. Обработчики инструментов получают Caller напрямую; это для собственных обработчиков go-sdk и тестов.
func CallerFrom(ctx context.Context, req mcp.Request) (Caller, bool) {
	if req != nil {
		if extra := req.GetExtra(); extra != nil && extra.TokenInfo != nil {
			if c, ok := extra.TokenInfo.Extra[callerKey].(Caller); ok {
				return c, true
			}
		}
	}
	return callerFromContext(ctx)
}

func (s *Server) toolHandler(t *tool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		c, ok := CallerFrom(ctx, req)
		if !ok {
			return errorResult(apierr.ToMCP(apierr.Unauthorized())), nil
		}
		out, aerr := s.run(ctx, c, t, req.Params.Arguments)
		if aerr != nil {
			return errorResult(aerr), nil
		}
		return &mcp.CallToolResult{
			StructuredContent: out,
			Content:           []mcp.Content{&mcp.TextContent{Text: string(out)}},
		}, nil
	}
}

func errorResult(e *apierr.Error) *mcp.CallToolResult {
	body, _ := json.Marshal(e) // строки и bool: ошибки быть не может
	return &mcp.CallToolResult{
		IsError:           true,
		StructuredContent: json.RawMessage(body),
		Content:           []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}
}

// filterToolList скрывает из tools/list инструменты, которые роли вызывающего вызывать нельзя (прямые вызовы получают
// forbidden).
func (s *Server) filterToolList(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		list, ok := res.(*mcp.ListToolsResult)
		if err != nil || !ok {
			return res, err
		}
		c, _ := CallerFrom(ctx, req)
		visible := make([]*mcp.Tool, 0, len(list.Tools))
		for _, mt := range list.Tools {
			if t := s.tools[mt.Name]; t != nil && t.allows(c.Role) {
				visible = append(visible, mt)
			}
		}
		filtered := *list
		filtered.Tools = visible
		return &filtered, nil
	}
}

// rpcErrUnauthorized — серверный код ошибки JSON-RPC, отправляемый с HTTP 401.
const rpcErrUnauthorized = -32001

func writeUnauthorized(w http.ResponseWriter) {
	e := apierr.Unauthorized()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      nil,
		"error":   map[string]any{"code": rpcErrUnauthorized, "message": e.Message, "data": e},
	})
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = fmt.Fprintf(w, "%s", body)
}
